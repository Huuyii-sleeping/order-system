package app

import (
	"context"
	"errors"
	"testing"
)

type dependencyPingerStub struct {
	err error
}

func (s dependencyPingerStub) PingContext(context.Context) error {
	return s.err
}

func TestServicesReadiness(t *testing.T) {
	t.Run("memory storage has no external dependency", func(t *testing.T) {
		if err := (&Services{}).CheckReadiness(context.Background()); err != nil {
			t.Fatalf("CheckReadiness() error = %v", err)
		}
	})

	t.Run("reports storage failure", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		services := &Services{readiness: dependencyPingerStub{err: wantErr}}

		err := services.CheckReadiness(context.Background())

		if !errors.Is(err, wantErr) {
			t.Fatalf("CheckReadiness() error = %v, want wrapped %v", err, wantErr)
		}
	})
}
