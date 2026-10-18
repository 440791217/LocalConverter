package camera

import "fmt"

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string                 { return e.Message }
func failure(status int, message string) error { return &Error{status, message} }
func newID() string                            { return fmt.Sprintf("%x", randomBytes(16)) }
