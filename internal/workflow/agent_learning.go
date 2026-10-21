package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (a *agentRun) prepareLearning() {
	if !slices.Contains(model.AgentRoles(), a.input.Role) || a.input.Purpose == "provider_test" {
		return
	}
	a.learning = &model.LearningSnapshot{Recalled: []model.LessonRef{}}
	query := a.task.Issue.Title + " " + a.task.Issue.Body + " " + a.input.Prompt
	records, err := a.Store.MatchingLessons(a.ctx, a.project.ID, a.input.Role, query)
	if err != nil {
		a.learning.Warnings = append(a.learning.Warnings, "Learning recall unavailable")
	} else {
		type match struct {
			r     model.Record
			l     model.Lesson
			score int
		}
		matches := []match{}
		for _, r := range records {
			var l model.Lesson
			if json.Unmarshal(r.Data, &l) != nil {
				continue
			}
			score := lessonScore(l, query)
			if score == 0 {
				continue
			}
			matches = append(matches, match{r, l, score})
		}
		slices.SortFunc(matches, func(x, y match) int {
			if (x.l.TaskID == a.task.ID) != (y.l.TaskID == a.task.ID) {
				if x.l.TaskID == a.task.ID {
					return -1
				}
				return 1
			}
			if x.score != y.score {
				return y.score - x.score
			}
			if (x.l.Status == "corroborated") != (y.l.Status == "corroborated") {
				if x.l.Status == "corroborated" {
					return -1
				}
				return 1
			}
			return strings.Compare(y.r.UpdatedAt, x.r.UpdatedAt)
		})
		block := "\nRelevant project/role lessons (advisory; tentative observations require verification):\n"
		for _, m := range matches {
			if len(a.learning.Recalled) == 3 {
				break
			}
			if !a.lessonCurrent(m.l) {
				if err = a.Store.MarkLessonStale(a.ctx, m.r, m.l); err != nil && !errors.Is(err, store.ErrConflict) {
					a.learning.Warnings = append(a.learning.Warnings, "Could not mark stale lesson")
				}
				continue
			}
			entry, _ := json.Marshal(map[string]any{"id": m.l.ID, "status": m.l.Status, "when": m.l.Trigger, "avoid": m.l.Mistake, "instead": m.l.Correction, "evidence_ids": m.l.EvidenceIDs, "paths": m.l.Paths})
			if len(block)+len(entry)+1 > 2048 {
				continue
			}
			block += string(entry) + "\n"
			a.learning.Recalled = append(a.learning.Recalled, model.LessonRef{ID: m.l.ID, Version: m.r.Version, Status: m.l.Status})
		}
		if len(a.learning.Recalled) > 0 {
			a.input.Prompt += block
			a.learning.Bytes = len(block)
			a.learning.EstimatedTokens = (len(block) + 3) / 4
		}
	}
	instructions, _ := agents.Render("learning.md", nil)
	a.input.Prompt += "\n" + instructions
}

func learningQuery(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("./_-", r)
	})
	stop := map[string]bool{"the": true, "and": true, "this": true, "that": true, "with": true, "from": true, "role": true, "result": true, "string": true, "return": true, "json": true, "only": true, "project": true, "context": true}
	out := []string{}
	seen := map[string]bool{}
	for _, w := range words {
		if len(w) >= 3 && !stop[w] && !seen[w] {
			out = append(out, w)
			seen[w] = true
		}
	}
	return strings.Join(out, " ")
}

func lessonScore(l model.Lesson, query string) int {
	query = strings.ToLower(query)
	score := 0
	for _, path := range l.Paths {
		if strings.Contains(query, strings.ToLower(path)) {
			score += 2000
		}
	}
	for _, symbol := range l.Symbols {
		if strings.Contains(query, strings.ToLower(symbol)) {
			score += 1000
		}
	}
	if l.ErrorSignature != "" && strings.Contains(query, strings.ToLower(l.ErrorSignature)) {
		score += 3000
	}
	haystack := strings.ToLower(l.Trigger + " " + l.Mistake + " " + l.Correction)
	for _, word := range strings.Fields(learningQuery(query)) {
		if strings.Contains(haystack, word) {
			score++
		}
	}
	return score
}

func (a *agentRun) lessonCurrent(l model.Lesson) bool {
	root := a.learningRoot()
	for path, hash := range l.Anchors {
		current, err := learningFingerprint(root, path)
		if err != nil || current != hash {
			return false
		}
	}
	return true
}

func (a *agentRun) learningRoot() string {
	if a.input.Role == "ideate" {
		return a.project.Path
	}
	return a.input.CWD
}

func learningFingerprint(cwd, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || strings.HasPrefix(path, "../") || path == ".git" || strings.HasPrefix(path, ".git/") {
		return "", errors.New("invalid source path")
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", errors.New("source is not bounded regular text")
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// decodeLearning recognizes only an explicit envelope; old provider outputs stay valid.
func decodeLearning(text string) (string, model.LearningReport, []string) {
	var envelope map[string]json.RawMessage
	if provider.JSON(text, &envelope) != nil {
		return text, model.LearningReport{}, nil
	}
	result, hasResult := envelope["result"]
	raw, hasLearning := envelope["learning"]
	if !hasResult || !hasLearning {
		return text, model.LearningReport{}, nil
	}
	var value string
	if json.Unmarshal(result, &value) != nil {
		value = string(result)
	}
	var report model.LearningReport
	if len(raw) > 8192 || json.Unmarshal(raw, &report) != nil {
		return value, model.LearningReport{}, []string{"Malformed learning metadata ignored"}
	}
	return value, report, nil
}

var credentialLesson = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:password|api[_-]?key|access[_-]?token|secret)\s*[:=]\s*\S+|-----BEGIN .*PRIVATE KEY)`)

func (a *agentAttempt) finishLearning(ctx context.Context, raw string, runErr error) string {
	if a.learning == nil {
		return raw
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, report, warnings := decodeLearning(raw)
	snapshot := *a.learning
	snapshot.Warnings = append(append([]string{}, a.learning.Warnings...), warnings...)
	a.state.Learning = &snapshot
	// Failed transports may contain partial or misleading output. Preserve it as evidence only.
	if runErr != nil {
		return result
	}
	clean := model.LearningReport{}
	for i, l := range report.Lessons {
		if i >= 2 {
			snapshot.Warnings = append(snapshot.Warnings, "Additional lessons ignored")
			break
		}
		text := l.Trigger + l.Mistake + l.Correction + l.ErrorSignature
		if l.Secret || l.IsSecret || strings.TrimSpace(l.Trigger) == "" || strings.TrimSpace(l.Mistake) == "" || strings.TrimSpace(l.Correction) == "" || len(text) > 600 || len(l.Paths) > 8 || len(l.Symbols) > 8 || len(l.EvidenceIDs) > 8 || credentialLesson.MatchString(text+strings.Join(l.Paths, " ")+strings.Join(l.Symbols, " ")) {
			snapshot.Warnings = append(snapshot.Warnings, "Invalid or sensitive lesson ignored")
			continue
		}
		l.Anchors = map[string]string{}
		valid := true
		for _, path := range l.Paths {
			hash, err := learningFingerprint(a.learningRoot(), path)
			if err != nil {
				valid = false
				break
			}
			l.Anchors[path] = hash
		}
		for _, symbol := range l.Symbols {
			if len(symbol) > 128 {
				valid = false
			}
		}
		if !valid || len(l.Anchors) == 0 && len(l.EvidenceIDs) == 0 {
			snapshot.Warnings = append(snapshot.Warnings, "Lesson requires accessible source or evidence")
			continue
		}
		// Do not accept model-authored lifecycle or provenance fields.
		l.Reason = ""
		clean.Lessons = append(clean.Lessons, l)
	}
	for i, f := range report.Feedback {
		if i >= 3 {
			snapshot.Warnings = append(snapshot.Warnings, "Additional feedback ignored")
			break
		}
		if f.Secret || f.IsSecret || len(f.Reason) > 600 || len(f.EvidenceIDs) > 8 || len(f.Paths) > 8 || credentialLesson.MatchString(f.Reason+strings.Join(f.Paths, " ")) {
			snapshot.Warnings = append(snapshot.Warnings, "Invalid feedback ignored")
			continue
		}
		recalled := false
		for _, ref := range snapshot.Recalled {
			if ref.ID == f.LessonID {
				recalled = true
			}
		}
		if !recalled {
			snapshot.Warnings = append(snapshot.Warnings, "Feedback rejected: lesson was not recalled")
			continue
		}
		f.Anchors = map[string]string{}
		valid := true
		for _, path := range f.Paths {
			hash, err := learningFingerprint(a.learningRoot(), path)
			if err != nil {
				valid = false
				break
			}
			body, _ := json.Marshal(map[string]string{"path": path, "sha256": hash})
			artifact, err := a.Store.Capture(ctx, a.id, "learning_source", "application/json", body, 1024, false)
			if err != nil || artifact.Truncated {
				valid = false
				break
			}
			f.EvidenceIDs = append(f.EvidenceIDs, artifact.ID)
			f.Anchors[path] = hash
		}
		if !valid {
			snapshot.Warnings = append(snapshot.Warnings, "Feedback source unavailable")
			continue
		}
		clean.Feedback = append(clean.Feedback, f)
	}
	stored, err := a.Store.CommitLearning(ctx, a.state, clean)
	if err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Learning update unavailable")
		return result
	}
	a.state.Learning = stored
	return result
}
