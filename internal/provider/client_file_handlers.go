package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"whirlwind/internal/policy"
)

func (c *Client) readFile(_ context.Context, _ Message, p clientParams) (any, error) {
	content, err := c.readFileContent(p.Path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	start := min(max(p.Line-1, 0), len(lines))
	end := len(lines)
	if p.Limit > 0 && start+p.Limit < end {
		end = start + p.Limit
	}
	return map[string]any{"content": strings.Join(lines[start:end], "\n")}, nil
}

func (c *Client) readFileContent(filePath string) ([]byte, error) {
	f, err := c.openFile(filePath, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readLimitedFile(f)
}

func (c *Client) openFile(filePath string, write bool) (*os.File, error) {
	path, err := c.path(filePath)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(c.input.CWD)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	return openACPFile(root, relative, write)
}

func readLimitedFile(f *os.File) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(content) > 1024*1024 {
		return nil, errors.New("file exceeds 1 MiB")
	}
	return content, nil
}

func (c *Client) writeFile(ctx context.Context, m Message, p clientParams) (any, error) {
	if err := c.writeFileContent(ctx, m, p); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func (c *Client) writeFileContent(ctx context.Context, m Message, p clientParams) error {
	f, err := c.openWritableFile(ctx, m, p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(p.Content)
	return err
}

func (c *Client) openWritableFile(ctx context.Context, m Message, p clientParams) (*os.File, error) {
	path, root, relative, err := c.writablePath(p.Path)
	if err != nil {
		return nil, err
	}
	if len(p.Content) > 1024*1024 {
		return nil, errors.New("file content exceeds 1 MiB")
	}
	return c.approveWritableFile(ctx, m, path, root, relative)
}

func (c *Client) writablePath(filePath string) (string, string, string, error) {
	path, err := c.path(filePath)
	if err != nil {
		return "", "", "", err
	}
	root, err := filepath.EvalSymlinks(c.input.CWD)
	if err != nil {
		return "", "", "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || !policy.AllowedPath(c.input.Policy, relative) {
		return "", "", "", errors.New("ACP write outside committed scope")
	}
	return path, root, relative, nil
}

func (c *Client) approveWritableFile(ctx context.Context, m Message, path, root, relative string) (*os.File, error) {
	if err := c.allow(ctx, Permission{Tool: m.Method, Paths: []string{path}, Raw: m.Params}); err != nil {
		return nil, err
	}
	return openACPFile(root, relative, true)
}
