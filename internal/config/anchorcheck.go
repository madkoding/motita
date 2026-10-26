package config

import (
	"fmt"
	"regexp"
)

func (c *Config) validateAnchor() error {
	if err := oneOf("anchor.kind", c.Anchor.Kind, "none", "command", "auto"); err != nil {
		return err
	}
	if c.Anchor.Kind == "command" {
		if err := requireField("anchor.kind=command", "command", c.Anchor.Command); err != nil {
			return err
		}
	}
	// A pattern that does not compile only failed when the check ran, so a typo went unnoticed
	// until a claim of done was judged by it.
	if _, err := regexp.Compile(c.Anchor.ExpectOutput); err != nil {
		return fmt.Errorf("anchor.expect_output is not a valid regular expression: %w", err)
	}
	for _, ch := range c.Anchor.Checks {
		if _, err := regexp.Compile(ch.ExpectOutput); err != nil {
			return fmt.Errorf("anchor.checks %q: expect_output is not a valid regular expression: %w", ch.Name, err)
		}
	}
	return nil
}
