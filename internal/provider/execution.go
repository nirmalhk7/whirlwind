package provider

import "whirlwind/internal/execution"

func (c *Client) executionProfile() (execution.Profile, error) {
	if c.input.Execution != nil {
		return *c.input.Execution, nil
	}
	p, err := execution.ForPolicy(c.input.CWD, c.input.ReadOnly, "", c.input.Policy.Sandbox)
	p.AllowedPaths = append([]string{}, c.input.Policy.AllowedPaths...)
	return p, err
}
