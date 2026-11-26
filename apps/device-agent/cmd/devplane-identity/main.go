//go:build linux

package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ai-dev-control-plane/device-agent/internal/deviceauth"
)

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: devplane-identity init|show --key /private/path/device.pem")
	}
	command := args[0]
	if command != "init" && command != "show" {
		return errors.New("unsupported identity command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("key", "", "path to an owner-only local Ed25519 identity file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || len(flags.Args()) != 0 {
		return errors.New("an explicit --key path is required")
	}
	var identity deviceauth.Identity
	var err error
	if command == "init" {
		identity, err = deviceauth.CreateIdentityFile(*path)
	} else {
		identity, err = deviceauth.LoadIdentityFile(*path)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "device_id=%s\npublic_key=%s\n", identity.ID, base64.RawURLEncoding.EncodeToString(identity.Public))
	return err
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "devplane-identity:", err)
		os.Exit(1)
	}
}
