package verification

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/model"
)

const qualityRuntimeByteLimit = int64(512 << 20)
const qualityRuntimeFileLimit = 100000

var qualityPackageDirectory = regexp.MustCompile(`^(.+)-([a-f0-9]{8,64})$`)
var qualityCatalogHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func isQltyCheckCommand(command []string) bool {
	return len(command) > 1 && filepath.Base(command[0]) == "qlty" && (command[1] == "check" || command[1] == "smells")
}

func hashQltyExecutable(executable string) (string, error) {
	return hashQltyExecutableInPath(executable, os.Getenv("PATH"))
}

func hashQltyExecutableInPath(executable, pathValue string) (string, error) {
	return hashQltyExecutableAt(executable, pathValue, "")
}

func hashQltyExecutableAt(executable, pathValue, cwd string) (string, error) {
	path := executable
	var err error
	if !filepath.IsAbs(path) {
		path, err = lookPathInCWD(path, pathValue, cwd)
	}
	if err != nil {
		return "", fmt.Errorf("locate executed Qlty binary: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executed Qlty binary: %w", err)
	}
	hash, _, err := hashRegularFile(path, 256<<20)
	return hash, err
}

func lookPathIn(executable, pathValue string) (string, error) {
	return lookPathInCWD(executable, pathValue, "")
}

func lookPathInCWD(executable, pathValue, cwd string) (string, error) {
	if strings.ContainsRune(executable, filepath.Separator) {
		if !filepath.IsAbs(executable) && cwd != "" {
			return filepath.Join(cwd, executable), nil
		}
		return executable, nil
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = "."
		}
		if !filepath.IsAbs(directory) && cwd != "" {
			directory = filepath.Join(cwd, directory)
		}
		candidate := filepath.Join(directory, executable)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in command PATH", executable)
}

func captureQualityIdentity(output, runtimeDir, executable string, snapshot *model.QualitySnapshot, expectedBinarySHA256 string) (*model.QualityIdentity, error) {
	catalogHash := ""
	if snapshot != nil {
		catalogHash, _ = HashQltyPluginCatalog(snapshot.PluginCatalog)
	}
	return captureQualityIdentityWithCatalog(output, runtimeDir, executable, snapshot, expectedBinarySHA256, catalogHash)
}

func captureQualityIdentityWithCatalog(output, runtimeDir, executable string, snapshot *model.QualitySnapshot, expectedBinarySHA256, observedCatalogHash string) (*model.QualityIdentity, error) {
	return captureQualityIdentityWithCatalogPath(output, runtimeDir, executable, snapshot, expectedBinarySHA256, observedCatalogHash, os.Getenv("PATH"))
}

func captureQualityIdentityWithCatalogPath(output, runtimeDir, executable string, snapshot *model.QualitySnapshot, expectedBinarySHA256, observedCatalogHash, pathValue string) (*model.QualityIdentity, error) {
	return captureQualityIdentityWithCatalogAt(output, runtimeDir, executable, snapshot, expectedBinarySHA256, observedCatalogHash, pathValue, "")
}

func captureQualityIdentityWithCatalogAt(output, runtimeDir, executable string, snapshot *model.QualitySnapshot, expectedBinarySHA256, observedCatalogHash, pathValue, cwd string) (*model.QualityIdentity, error) {
	if snapshot == nil || len(output) == 0 || len(output) > 1<<20 || runtimeDir == "" {
		return nil, errors.New("missing frozen configuration, SARIF, or isolated runtime directory")
	}
	if err := validateFrozenRuleSources(snapshot.Files); err != nil {
		return nil, err
	}
	version, err := qltyReportVersion(output)
	if err != nil {
		return nil, err
	}
	qltyHash, err := hashQltyExecutableAt(executable, pathValue, cwd)
	if err != nil {
		return nil, fmt.Errorf("hash Qlty binary: %w", err)
	}
	if expectedBinarySHA256 != "" && qltyHash != expectedBinarySHA256 {
		return nil, errors.New("executed Qlty binary changed during scan")
	}
	configurationHash, err := qualityConfigurationHash(snapshot.Files)
	if err != nil {
		return nil, err
	}
	catalogHash := observedCatalogHash
	if snapshot.Identity != nil {
		catalogHash = snapshot.Identity.PluginCatalogHash
	} else if !qualityCatalogHashPattern.MatchString(catalogHash) {
		return nil, errors.New("effective Qlty plugin catalog was not verified")
	}
	toolRoot := filepath.Join(runtimeDir, "home", ".qlty", "cache", "tools")
	tools, err := installedQualityTools(toolRoot)
	if err != nil {
		return nil, err
	}
	runtimeHash, err := hashQualityTree(toolRoot)
	if err != nil {
		return nil, fmt.Errorf("hash installed Qlty scanner runtime: %w", err)
	}
	runtimeIdentityPath, err := filepath.EvalSymlinks(toolRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve installed Qlty scanner runtime path: %w", err)
	}
	if snapshot.Identity != nil {
		runtimeIdentityPath = snapshot.Identity.ToolRuntimePath
	}
	identity := &model.QualityIdentity{Version: 1, QltyVersion: version, QltyBinarySHA256: qltyHash, ConfigurationHash: configurationHash, ToolRuntimeHash: runtimeHash, PluginCatalogHash: catalogHash, ToolRuntimePath: runtimeIdentityPath, Tools: tools}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return identity, nil
}

// HashQltyPluginCatalog returns a stable digest of the effective plugin
// definitions reported by `qlty config show --no-upgrade-check`.
func HashQltyPluginCatalog(output []byte) (string, error) {
	if len(output) == 0 || len(output) > 4<<20 {
		return "", errors.New("missing or oversized Qlty plugin catalog evidence")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(output, &document); err != nil || len(document.Content) != 1 {
		return "", errors.New("Qlty plugin catalog is not valid YAML")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("Qlty plugin catalog has no mapping root")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "plugins" {
			plugins := root.Content[i+1]
			canonicalizeQltyCatalogNode(plugins)
			canonical, err := yaml.Marshal(plugins)
			if err != nil || len(canonical) == 0 {
				return "", errors.New("Qlty plugin catalog is empty or invalid")
			}
			hash := sha256.Sum256(canonical)
			return hex.EncodeToString(hash[:]), nil
		}
	}
	return "", errors.New("Qlty config output lacks effective plugins catalog")
}

func canonicalizeQltyCatalogNode(node *yaml.Node) {
	for _, child := range node.Content {
		canonicalizeQltyCatalogNode(child)
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	pairs := make([][2]*yaml.Node, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		pairs = append(pairs, [2]*yaml.Node{node.Content[i], node.Content[i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0].Value < pairs[j][0].Value })
	node.Content = node.Content[:0]
	for _, pair := range pairs {
		node.Content = append(node.Content, pair[0], pair[1])
	}
}

var remoteRuleReference = regexp.MustCompile(`(?i)(https?://|git@)`)
var astGrepRemoteRuleReference = regexp.MustCompile(`(?im)^\s*(?:config|configs|include)\s*:\s*["']?(?:https?://|git@)`)

func validateFrozenRuleSources(files map[string]string) error {
	for path, content := range files {
		if strings.HasPrefix(path, ".qlty/") && remoteRuleReference.MatchString(content) {
			return errors.New("external Qlty sources and remote rule references cannot be frozen; use approved local rule files")
		}
		if path == "sgconfig.yml" && astGrepRemoteRuleReference.MatchString(content) {
			return errors.New("ast-grep configuration contains a remote rule reference; freeze rules locally")
		}
	}
	return nil
}

func qltyReportVersion(output string) (string, error) {
	var report struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name            string `json:"name"`
					Version         string `json:"version"`
					SemanticVersion string `json:"semanticVersion"`
				} `json:"driver"`
			} `json:"tool"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || report.Version != "2.1.0" || len(report.Runs) == 0 {
		return "", errors.New("Qlty SARIF lacks complete runtime version evidence")
	}
	version := ""
	for _, run := range report.Runs {
		driver := run.Tool.Driver
		if driver.Name != "qlty" || strings.TrimSpace(driver.Version) == "" || strings.TrimSpace(driver.SemanticVersion) == "" || strings.ContainsAny(driver.Version+driver.SemanticVersion, "\x00\r\n") {
			return "", errors.New("Qlty SARIF lacks Qlty driver version")
		}
		if version != "" && version != driver.Version {
			return "", errors.New("Qlty SARIF contains incompatible driver versions")
		}
		version = driver.Version
	}
	return version, nil
}

func qualityConfigurationHash(files map[string]string) (string, error) {
	if len(files) == 0 || len(files) > 32 {
		return "", errors.New("frozen quality configuration is missing")
	}
	paths := make([]string, 0, len(files))
	for path, content := range files {
		if !model.QualityConfigurationPathAllowed(path) || content == "" || len(content) > 1<<20 {
			return "", errors.New("invalid frozen quality configuration identity input")
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = io.WriteString(hash, path)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, files[path])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func installedQualityTools(root string) ([]model.QualityToolIdentity, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read installed Qlty scanner runtimes: %w", err)
	}
	tools := []model.QualityToolIdentity{}
	for _, toolEntry := range entries {
		if !toolEntry.IsDir() || strings.HasSuffix(toolEntry.Name(), "-installation-debug-files") {
			continue
		}
		toolName := toolEntry.Name()
		if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(toolName) {
			return nil, fmt.Errorf("invalid installed Qlty tool name %q", toolName)
		}
		packages, err := os.ReadDir(filepath.Join(root, toolName))
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			if !pkg.IsDir() || strings.HasSuffix(pkg.Name(), "-installation-debug-files") {
				continue
			}
			match := qualityPackageDirectory.FindStringSubmatch(pkg.Name())
			if match == nil || strings.TrimSpace(match[1]) == "" {
				return nil, fmt.Errorf("unrecognized installed Qlty tool package %s/%s", toolName, pkg.Name())
			}
			marker := filepath.Join(root, toolName, pkg.Name()+".done")
			info, err := os.Lstat(marker)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("Qlty tool package %s/%s lacks completed-install marker", toolName, pkg.Name())
			}
			hasAssets, err := qualityPackageHasAssets(filepath.Join(root, toolName, pkg.Name()))
			if err != nil || !hasAssets {
				return nil, fmt.Errorf("Qlty tool package %s/%s contains no runtime assets", toolName, pkg.Name())
			}
			tools = append(tools, model.QualityToolIdentity{Name: toolName, Version: match[1], Fingerprint: match[2]})
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name+"@"+tools[i].Version < tools[j].Name+"@"+tools[j].Version
	})
	if len(tools) == 0 {
		return nil, errors.New("Qlty installed no scanner runtime packages")
	}
	return tools, nil
}

func qualityPackageHasAssets(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			found = true
		}
		return nil
	})
	return found, err
}

func hashQualityTree(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Qlty scanner runtime directory is missing or unsafe")
	}
	rooted, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = rooted.Close() }()
	hash := sha256.New()
	var total int64
	count := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasSuffix(entry.Name(), "-installation-debug-files") {
			return filepath.SkipDir
		}
		count++
		if count > qualityRuntimeFileLimit {
			return errors.New("Qlty scanner runtime exceeds file-count limit")
		}
		writeQualityRecordField(hash, []byte(filepath.ToSlash(relative)))
		info, err := rooted.Lstat(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		var mode [4]byte
		binary.BigEndian.PutUint32(mode[:], uint32(info.Mode().Perm()&^0222))
		writeQualityRecordField(hash, mode[:])
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := rooted.Readlink(filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			canonicalTarget, err := rootedQualityLinkTarget(rooted, relative, target)
			if err != nil {
				return fmt.Errorf("Qlty scanner runtime symlink %s -> %q is unsafe: %w", filepath.ToSlash(relative), target, err)
			}
			writeQualityRecordField(hash, []byte("symlink"))
			writeQualityRecordField(hash, []byte(filepath.ToSlash(canonicalTarget)))
			return nil
		}
		if info.IsDir() {
			writeQualityRecordField(hash, []byte("directory"))
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("Qlty scanner runtime contains a special file")
		}
		writeQualityRecordField(hash, []byte("file"))
		file, err := rooted.Open(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		fileHash := sha256.New()
		written, copyErr := io.Copy(fileHash, io.LimitReader(file, qualityRuntimeByteLimit-total+1))
		closeErr := file.Close()
		total += written
		if total > qualityRuntimeByteLimit {
			return errors.New("Qlty scanner runtime exceeds byte limit")
		}
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(written))
		writeQualityRecordField(hash, size[:])
		writeQualityRecordField(hash, fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func rootedQualityLinkTarget(root *os.Root, linkPath, target string) (string, error) {
	var rootedTarget string
	if filepath.IsAbs(target) {
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
		rootName, err := filepath.EvalSymlinks(root.Name())
		if err != nil {
			return "", err
		}
		rootedTarget, err = filepath.Rel(rootName, resolved)
		if err != nil || !filepath.IsLocal(rootedTarget) {
			return "", errors.New("absolute symlink escapes runtime root")
		}
	} else {
		rootedTarget = filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
		if !filepath.IsLocal(rootedTarget) {
			return "", errors.New("relative symlink escapes runtime root")
		}
	}
	if _, err := root.Lstat(filepath.ToSlash(rootedTarget)); err != nil {
		return "", err
	}
	return rootedTarget, nil
}

func copyApprovedQualityTree(ctx context.Context, source, destination, expectedHash string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || qualityPathsOverlap(source, destination) || filepath.Base(source) != "tools" {
		return errors.New("approved Qlty runtime paths must be distinct absolute paths")
	}
	before, err := hashQualityTree(source)
	if err != nil || before != expectedHash {
		return errors.New("approved Qlty scanner runtime changed before copy")
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("candidate Qlty scanner runtime destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer func() { _ = sourceRoot.Close() }()
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	total, count := int64(0), 0
	type directoryMode struct {
		path string
		mode os.FileMode
	}
	directories := []directoryMode{}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || !filepath.IsLocal(relative) {
			return errors.New("unsafe scanner runtime path")
		}
		count++
		if count > qualityRuntimeFileLimit {
			return errors.New("approved scanner runtime exceeds file-count limit")
		}
		info, err := sourceRoot.Lstat(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		destinationPath := filepath.Join(destination, relative)
		switch {
		case info.IsDir():
			if err := os.Mkdir(destinationPath, 0700); err != nil {
				return err
			}
			directories = append(directories, directoryMode{path: destinationPath, mode: info.Mode().Perm()})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			target, err := sourceRoot.Readlink(filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			canonical, err := rootedQualityLinkTarget(sourceRoot, relative, target)
			if err != nil {
				return err
			}
			link, err := filepath.Rel(filepath.Dir(destinationPath), filepath.Join(destination, canonical))
			if err != nil {
				return err
			}
			return os.Symlink(link, destinationPath)
		case info.Mode().IsRegular():
			input, err := sourceRoot.Open(filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			output, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&^0222)
			if err != nil {
				_ = input.Close()
				return err
			}
			written, copyErr := io.Copy(output, io.LimitReader(contextReader{ctx: ctx, reader: input}, qualityRuntimeByteLimit-total+1))
			total += written
			if total > qualityRuntimeByteLimit {
				copyErr = errors.Join(copyErr, errors.New("approved scanner runtime exceeds byte limit"))
			}
			return errors.Join(copyErr, input.Close(), output.Close())
		default:
			return errors.New("Qlty scanner runtime contains a special file")
		}
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index].path, directories[index].mode&^0222); err != nil {
			return err
		}
	}
	if err := os.Chmod(destination, 0500); err != nil {
		return err
	}
	after, err := hashQualityTree(source)
	if err != nil || after != expectedHash {
		return errors.New("approved Qlty scanner runtime changed during copy")
	}
	copied, err := hashQualityTree(destination)
	if err != nil || copied != expectedHash {
		return errors.New("copied Qlty scanner runtime failed identity verification")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

func writeQualityRecordField(dst io.Writer, field []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = dst.Write(length[:])
	_, _ = dst.Write(field)
}

func hashRegularFile(path string, limit int64) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "", 0, errors.New("file is missing, not regular, or exceeds hash limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(hash, io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", 0, err
	}
	if count > limit {
		return "", 0, errors.New("file exceeds hash limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}
