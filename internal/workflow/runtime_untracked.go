package workflow

import (
	"strings"
	"whirlwind/internal/github"
)

func (a *agentAttempt) untrackedProgress() (string, error) {
	files, err := github.Run(a.ctx, a.input.CWD, "git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	if files == "" {
		return "", nil
	}
	var fingerprints strings.Builder
	for _, path := range strings.Split(strings.TrimSuffix(files, "\x00"), "\x00") {
		hash, err := learningFingerprint(a.input.CWD, path)
		if err != nil {
			return "", err
		}
		fingerprints.WriteString(path)
		fingerprints.WriteByte(0)
		fingerprints.WriteString(hash)
		fingerprints.WriteByte(0)
	}
	return fingerprints.String(), nil
}
