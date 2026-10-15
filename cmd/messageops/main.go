// Command messageops performs bounded, auditable message recovery operations.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"example.com/order-system/internal/adapters/idgenerator"
	"example.com/order-system/internal/adapters/messaging"
	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/application/messageops"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return usageError()
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
	store := postgres.NewMessageOperationsStore(db)
	authorizer := authorization.NewService(postgres.NewIdentityStore(db), authorization.Policy{})

	switch arguments[0] {
	case "outbox-inspect":
		flags := flag.NewFlagSet("outbox-inspect", flag.ContinueOnError)
		messageID := flags.String("message-id", "", "exact outbox message ID")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		service := messageops.NewService(store, nil, idgenerator.Random{}, nil)
		record, err := service.InspectOutbox(ctx, *messageID)
		return printResult(record, err)

	case "inbox-inspect":
		flags := flag.NewFlagSet("inbox-inspect", flag.ContinueOnError)
		messageID := flags.String("message-id", "", "exact business message ID")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		service := messageops.NewService(store, nil, idgenerator.Random{}, nil)
		record, err := service.InspectInbox(ctx, *messageID)
		return printResult(record, err)

	case "outbox-retry":
		flags := flag.NewFlagSet("outbox-retry", flag.ContinueOnError)
		messageID := flags.String("message-id", "", "exact outbox message ID")
		expectedAttempts := flags.Int("expected-attempts", -1, "attempt count observed during inspection")
		actor := flags.String("actor", "", "operator identity")
		reason := flags.String("reason", "", "ticket or recovery reason")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		service := messageops.NewService(store, nil, idgenerator.Random{}, nil).WithActorAuthorization(authorizer)
		action, err := service.RetryOutbox(ctx, *messageID, *expectedAttempts, *actor, *reason)
		return printResult(action, err)

	case "dlq-replay":
		flags := flag.NewFlagSet("dlq-replay", flag.ContinueOnError)
		topic := flags.String("topic", "", "dead-letter topic")
		partition := flags.Int("partition", -1, "dead-letter partition")
		offset := flags.String("offset", "", "dead-letter offset")
		actor := flags.String("actor", "", "operator identity")
		reason := flags.String("reason", "", "ticket or recovery reason")
		allowedTopic := flags.String("allowed-topic", "", "expected original business topic")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		parsedOffset, err := strconv.ParseInt(*offset, 10, 64)
		if err != nil {
			return errors.New("offset must be a non-negative integer")
		}
		brokers := splitNonEmpty(os.Getenv("KAFKA_BROKERS"))
		replayer, err := messaging.NewDeadLetterReplayer(brokers)
		if err != nil {
			return err
		}
		defer replayer.Close()
		service := messageops.NewService(store, replayer, idgenerator.Random{}, []string{*allowedTopic}).WithActorAuthorization(authorizer)
		action, err := service.ReplayDeadLetter(
			ctx, *topic, int32(*partition), parsedOffset, *actor, *reason,
		)
		return printResult(action, err)

	default:
		return usageError()
	}
}

func printResult(value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func usageError() error {
	return errors.New("usage: messageops <outbox-inspect|outbox-retry|inbox-inspect|dlq-replay> [flags]")
}
