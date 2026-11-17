// Package httpx holds the shared HTTP response shapes.
//
// Every handler answers errors in one envelope (PRD §36) so the frontend has
// exactly one error path to implement.
package httpx

import "github.com/gin-gonic/gin"

// Error codes from PRD §36.
const (
	CodeMeetingNotFound    = "MEETING_NOT_FOUND"
	CodeInvalidAudio       = "INVALID_AUDIO"
	CodeChunkAlreadyExists = "CHUNK_ALREADY_EXISTS"
	CodeSTTUnavailable     = "STT_UNAVAILABLE"
	CodeSTTFailed          = "STT_FAILED"
	CodeSummaryFailed      = "SUMMARY_FAILED"
	CodeInvalidRequest     = "INVALID_REQUEST"
	CodeInternalError      = "INTERNAL_ERROR"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// Fail writes the standard error envelope and stops the handler chain.
func Fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error: ErrorBody{Code: code, Message: message},
	})
}
