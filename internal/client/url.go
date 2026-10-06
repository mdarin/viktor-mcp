package client

import "fmt"

func url(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
