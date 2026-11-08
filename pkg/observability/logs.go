package observability

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

func ConfigureLogging(level, format string) error {
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("logs.level: %w", err)
	}
	logrus.SetLevel(lvl)

	switch format {
	case "json":
		logrus.SetFormatter(&logrus.JSONFormatter{})
	case "text":
		logrus.SetFormatter(&logrus.TextFormatter{})
	default:
		return fmt.Errorf("logs.format: unknown format %q", format)
	}
	return nil
}
