package verification

import "slices"

// Qlty filters these reports to changed lines relative to Git HEAD. Every
// reported finding is therefore blocking; no old scanner report is needed.
func IsQltyHeadDiffCommand(command []string) bool {
	if !baselineQlty(Result{Command: command}) || slices.Contains(command, "--all") || slices.Contains(command, "-a") {
		return false
	}
	for index, argument := range command {
		if argument == "--" {
			break
		}
		if argument == "--upstream=HEAD" || argument == "--upstream" && index+1 < len(command) && command[index+1] == "HEAD" {
			return true
		}
	}
	return false
}
