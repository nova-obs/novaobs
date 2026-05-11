package apperr

import "net/http"

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e Error) Error() string {
	return e.Message
}

func NotFound(message string) Error {
	return Error{Status: http.StatusNotFound, Code: "not_found", Message: message}
}

func InvalidRequest(message string) Error {
	return Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: message}
}

func Conflict(message string) Error {
	return Error{Status: http.StatusConflict, Code: "conflict", Message: message}
}
