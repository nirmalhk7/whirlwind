package verification

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"whirlwind/internal/process"
)

const offlineSourceLimit = 64 << 20

type offlineBlob struct {
	path, hash string
	mode       os.FileMode
}

func (r *jobRunner) exportOfflineSource(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	m := r.manifest
	tree, err := r.git(ctx, m.Repository, "rev-parse", m.CandidateCommit+"^{tree}")
	if err != nil || tree != m.CandidateTree {
		return "", errors.New("offline verifier candidate tree mismatch")
	}
	if _, err := r.git(ctx, m.Repository, "cat-file", "-e", m.BaseCommit+"^{commit}"); err != nil {
		return "", err
	}
	list, err := r.gitRaw(ctx, m.Repository, "ls-tree", "-r", "--full-tree", "-z", tree)
	if err != nil {
		return "", err
	}
	blobs, err := offlineTreeBlobs(list)
	if err != nil {
		return "", err
	}
	raw, err := r.readOfflineBlobs(ctx, m.Repository, blobs)
	if err != nil {
		return "", err
	}
	defer func() { _ = raw.Close(); _ = os.Remove(raw.Name()) }()
	stage, err := os.MkdirTemp(r.root.Name(), "offline-checkout-")
	if err != nil {
		return "", err
	}
	if err := unpackOfflineBlobs(ctx, stage, raw, blobs); err != nil {
		_ = os.RemoveAll(stage)
		return "", err
	}
	return stage, nil
}

func offlineTreeBlobs(list string) ([]offlineBlob, error) {
	var blobs []offlineBlob
	for entry := range strings.SplitSeq(strings.TrimSuffix(list, "\x00"), "\x00") {
		meta, path, found := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !found || len(fields) != 3 || fields[1] != "blob" || !gitObject.MatchString(fields[2]) || !filepath.IsLocal(path) || filepath.Clean(path) != path {
			return nil, errors.New("offline verifier requires a nonempty tree of regular Git blobs")
		}
		blob := offlineBlob{path: path, hash: fields[2], mode: 0444}
		switch fields[0] {
		case "100644":
		case "100755":
			blob.mode = 0555
		default:
			return nil, errors.New("offline verifier supports regular tracked files only")
		}
		blobs = append(blobs, blob)
	}
	return blobs, nil
}

func (r *jobRunner) readOfflineBlobs(ctx context.Context, repository string, blobs []offlineBlob) (*os.File, error) {
	raw, err := os.CreateTemp(r.root.Name(), "offline-blobs-")
	if err != nil {
		return nil, err
	}
	var input strings.Builder
	for _, blob := range blobs {
		input.WriteString(blob.hash + "\n")
	}
	// Request raw objects by ID. Checkout and archive conversions can invoke
	// repository-configured filters on the host.
	cmd := r.gitCommand(ctx, repository, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stdout, cmd.Stderr = &offlineSourceStream{destination: raw}, io.Discard
	if err := process.Run(cmd); err != nil {
		_ = raw.Close()
		_ = os.Remove(raw.Name())
		return nil, fmt.Errorf("read exact Git blobs for offline verifier: %w", err)
	}
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		_ = raw.Close()
		_ = os.Remove(raw.Name())
		return nil, err
	}
	return raw, nil
}

func unpackOfflineBlobs(ctx context.Context, stage string, raw io.Reader, blobs []offlineBlob) error {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	reader := bufio.NewReader(raw)
	for _, blob := range blobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.ReadSlice('\n')
		fields := strings.Fields(string(header))
		if err != nil || len(fields) != 3 || fields[0] != blob.hash || fields[1] != "blob" {
			return errors.New("offline export omitted a Git blob or changed its identity")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > offlineSourceLimit {
			return errors.New("offline export has an invalid Git blob size")
		}
		if err := writeOfflineBlob(root, reader, blob, size); err != nil {
			return err
		}
		if end, err := reader.ReadByte(); err != nil || end != '\n' {
			return errors.New("offline export has invalid Git blob framing")
		}
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return errors.New("offline export includes unexpected Git data")
	}
	return nil
}

func writeOfflineBlob(root *os.Root, reader io.Reader, blob offlineBlob, size int64) error {
	if err := root.MkdirAll(filepath.Dir(blob.path), 0755); err != nil {
		return err
	}
	var digest hash.Hash = sha1.New() // SHA-1 is the Git object format for 40-hex IDs.
	if len(blob.hash) == 64 {
		digest = sha256.New()
	}
	_, _ = fmt.Fprintf(digest, "blob %d\x00", size)
	file, err := root.OpenFile(blob.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, blob.mode)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(io.MultiWriter(file, digest), reader, size)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != blob.hash {
		return fmt.Errorf("offline export content does not match Git blob for %q", blob.path)
	}
	return nil
}

type offlineSourceStream struct {
	destination io.Writer
	written     int
}

func (stream *offlineSourceStream) Write(p []byte) (int, error) {
	if stream.written+len(p) > offlineSourceLimit {
		return 0, errors.New("offline source export exceeds 64 MiB")
	}
	n, err := stream.destination.Write(p)
	stream.written += n
	return n, err
}
