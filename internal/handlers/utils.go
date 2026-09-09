package handlers

import "fmt"

func ErrMsg(msg string) []byte {
	return []byte(fmt.Sprintf(`{"error":"%s"}`, msg))
}
