package runtimes

import (
	"fmt"
	"strings"
)

// allowedCommandNames is the set of commands that may be executed directly
// without a shell. The list is intentionally restrictive.
var allowedCommandNames = map[string]bool{
	"git":    true,
	"go":     true,
	"npm":    true,
	"yarn":   true,
	"pnpm":   true,
	"node":   true,
	"python": true,
	"python3": true,
	"pytest": true,
	"make":   true,
	"cargo":  true,
	"rustc":  true,
	"rg":     true,
	"grep":   true,
	"cat":    true,
	"ls":     true,
	"pwd":    true,
	"sleep":  true,
	"find":   true,
	"sed":    true,
	"head":   true,
	"tail":   true,
	"wc":     true,
	"echo":   true,
	"test":   true,
	"[" :     true,
}

// dangerousShellMetacharacters are characters that would enable shell escaping
// or command chaining if a string were ever passed to a shell.
var dangerousShellMetacharacters = []string{
	";", "|", "&", "$", "`", "\\", ">", "<", "(", ")", "{", "}", "*", "?",
}

// ValidateCommandArgs validates a command argv array for safe direct execution.
func ValidateCommandArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command args are empty")
	}
	name := args[0]
	if !allowedCommandNames[name] {
		return fmt.Errorf("command %q is not in the allow-list", name)
	}
	for _, arg := range args[1:] {
		for _, char := range dangerousShellMetacharacters {
			if strings.Contains(arg, char) {
				return fmt.Errorf("argument %q contains shell metacharacter %q", arg, char)
			}
		}
	}
	return nil
}

// ParseCommandString splits a simple command string into argv and validates it.
// It rejects strings containing shell metacharacters and only allows commands
// in the allow-list.
func ParseCommandString(s string) ([]string, error) {
	for _, char := range dangerousShellMetacharacters {
		if strings.Contains(s, char) {
			return nil, fmt.Errorf("command contains shell metacharacter %q", char)
		}
	}
	args := strings.Fields(s)
	if err := ValidateCommandArgs(args); err != nil {
		return nil, err
	}
	return args, nil
}
