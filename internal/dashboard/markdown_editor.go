package dashboard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// editMarkdown edits a disposable document copy; only the supervisor can save
// published bytes or change approval state.
func (d *Dashboard) editMarkdown(content string) (string, bool) {
	file, err := os.CreateTemp("", "whirlwind-plan-*.md")
	if err != nil {
		d.status.SetText("Whirlwind | cannot open editor: " + err.Error())
		return "", false
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, err = file.WriteString(content)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && !d.Suspend(func() {
		command := exec.Command("vim", "-n", "-c", "setlocal filetype=markdown", "-c", "syntax enable", "--", file.Name())
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = command.Run()
	}) {
		err = fmt.Errorf("terminal cannot be suspended")
	}
	if err != nil {
		d.status.SetText("Whirlwind | edit cancelled; saved plan unchanged")
		return "", false
	}
	input, err := os.Open(file.Name())
	if err != nil {
		d.status.SetText("Whirlwind | cannot read edited plan: " + err.Error())
		return "", false
	}
	defer func() { _ = input.Close() }()
	edited, err := io.ReadAll(io.LimitReader(input, 512*1024+1))
	if err != nil || len(edited) > 512*1024 {
		d.status.SetText("Whirlwind | edited document cannot exceed 512 KiB")
		return "", false
	}
	if string(edited) == content {
		d.status.SetText("Whirlwind | plan unchanged")
		return "", false
	}
	return string(edited), true
}
