package workflow

import (
	"errors"
	"fmt"
	"strings"
)

type planMarkdownParser struct {
	objective, current, fence string
	sections                  map[string]string
}

func splitPlanDocument(text string) (string, map[string]string, error) {
	parser := planMarkdownParser{sections: map[string]string{}}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if err := parser.consume(line); err != nil {
			return "", nil, err
		}
	}
	if parser.objective == "" || parser.fence != "" {
		return "", nil, errors.New("plan requires an objective title and closed code fences")
	}
	for heading, content := range parser.sections {
		parser.sections[heading] = strings.TrimSpace(content)
	}
	return parser.objective, parser.sections, nil
}

func (p *planMarkdownParser) consume(line string) error {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
		marker := trimmed[:3]
		switch p.fence {
		case "":
			p.fence = marker
		case marker:
			p.fence = ""
		}
		return p.appendLine(line)
	}
	if p.fence == "" {
		if strings.HasPrefix(trimmed, "# ") {
			return p.title(strings.TrimSpace(strings.TrimPrefix(trimmed, "# ")))
		}
		if strings.HasPrefix(trimmed, "## ") {
			return p.section(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))))
		}
	}
	return p.appendLine(line)
}

func (p *planMarkdownParser) title(title string) error {
	if p.objective != "" || p.current != "" {
		return errors.New("plan requires one objective title")
	}
	p.objective = title
	return nil
}

func (p *planMarkdownParser) section(heading string) error {
	if _, found := p.sections[heading]; found {
		return fmt.Errorf("duplicate plan section: %s", heading)
	}
	p.current, p.sections[heading] = heading, ""
	return nil
}

func (p *planMarkdownParser) appendLine(line string) error {
	if p.current != "" {
		p.sections[p.current] += line + "\n"
		return nil
	}
	if strings.TrimSpace(line) != "" {
		return errors.New("plan prose must follow a section heading")
	}
	return nil
}
