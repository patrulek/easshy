package easshy

import (
	"context"
	"fmt"
)

// Option let you start shell session with additional properties.
type Option func(context.Context, *shell) error

// WithShellContext will set given shell context to session or command.
func WithShellContext(shCtx ShellContext) Option {
	return func(ctx context.Context, s *shell) error {
		for env, value := range shCtx.Env {
			cmd := fmt.Sprintf("export %s=%s", env, value)

			if err := s.write(cmd); err != nil {
				return err
			}

			if _, err := s.read(ctx); err != nil {
				return err
			}
		}

		if shCtx.Path != "" {
			cmd := fmt.Sprintf("cd %s", shCtx.Path)

			if err := s.write(cmd); err != nil {
				return err
			}

			if _, err := s.read(ctx); err != nil {
				return err
			}
		}

		return nil
	}
}
