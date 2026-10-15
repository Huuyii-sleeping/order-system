// Command accessops bootstraps and changes administrative roles with an audit trail.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"example.com/order-system/internal/adapters/idgenerator"
	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/authorization"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "role-set" {
		return errors.New("usage: accessops role-set --user-id ID --role ROLE --actor ID --reason TEXT")
	}
	flags := flag.NewFlagSet("role-set", flag.ContinueOnError)
	userID := flags.String("user-id", "", "target user ID")
	roleValue := flags.String("role", "", "customer, operator or admin")
	actor := flags.String("actor", "", "operator identity")
	reason := flags.String("reason", "", "ticket or provisioning reason")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	role, ok := authorization.ParseRole(*roleValue)
	if !ok {
		return errors.New("role must be customer, operator or admin")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	eventID, err := (idgenerator.Random{}).NewID()
	if err != nil {
		return fmt.Errorf("generate role audit event ID: %w", err)
	}
	if err := postgres.NewIdentityStore(db).SetUserRole(
		ctx, eventID, *actor, *userID, role, *reason, time.Now().UTC(),
	); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "role updated; audit_event_id=%s\n", eventID)
	return nil
}
