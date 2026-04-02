package news

import "fmt"

// NewsError represents an error in the news system.
type NewsError struct {
	Code    string
	Message string
	Err     error
}

// Error returns the error message.
func (e *NewsError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying error.
func (e *NewsError) Unwrap() error {
	return e.Err
}

// Error codes.
const (
	ErrCodeFetchFailed          = "FETCH_FAILED"
	ErrCodeParseFailed          = "PARSE_FAILED"
	ErrCodeFeedNotFound         = "FEED_NOT_FOUND"
	ErrCodeFeedExists           = "FEED_EXISTS"
	ErrCodeItemNotFound         = "ITEM_NOT_FOUND"
	ErrCodeInvalidURL           = "INVALID_URL"
	ErrCodeInvalidFeedType      = "INVALID_FEED_TYPE"
	ErrCodeSubscriptionNotFound = "SUBSCRIPTION_NOT_FOUND"
	ErrCodeDatabase             = "DATABASE_ERROR"
)

// NewFetchError creates a new fetch error.
func NewFetchError(err error, url string) *NewsError {
	return &NewsError{
		Code:    ErrCodeFetchFailed,
		Message: fmt.Sprintf("failed to fetch feed %s", url),
		Err:     err,
	}
}

// NewParseError creates a new parse error.
func NewParseError(err error, feedType string) *NewsError {
	return &NewsError{
		Code:    ErrCodeParseFailed,
		Message: fmt.Sprintf("failed to parse %s feed", feedType),
		Err:     err,
	}
}

// NewFeedNotFoundError creates a new feed not found error.
func NewFeedNotFoundError(id int64) *NewsError {
	return &NewsError{
		Code:    ErrCodeFeedNotFound,
		Message: fmt.Sprintf("feed %d not found", id),
	}
}

// NewFeedExistsError creates a new feed exists error.
func NewFeedExistsError(url string) *NewsError {
	return &NewsError{
		Code:    ErrCodeFeedExists,
		Message: fmt.Sprintf("feed with URL %s already exists", url),
	}
}

// NewItemNotFoundError creates a new item not found error.
func NewItemNotFoundError(id int64) *NewsError {
	return &NewsError{
		Code:    ErrCodeItemNotFound,
		Message: fmt.Sprintf("item %d not found", id),
	}
}

// NewInvalidURLError creates a new invalid URL error.
func NewInvalidURLError(url string) *NewsError {
	return &NewsError{
		Code:    ErrCodeInvalidURL,
		Message: fmt.Sprintf("invalid URL: %s", url),
	}
}

// NewInvalidFeedTypeError creates a new invalid feed type error.
func NewInvalidFeedTypeError(feedType string) *NewsError {
	return &NewsError{
		Code:    ErrCodeInvalidFeedType,
		Message: fmt.Sprintf("invalid feed type: %s", feedType),
	}
}

// NewSubscriptionNotFoundError creates a new subscription not found error.
func NewSubscriptionNotFoundError(chatID, feedID int64) *NewsError {
	return &NewsError{
		Code:    ErrCodeSubscriptionNotFound,
		Message: fmt.Sprintf("subscription not found: chat_id=%d, feed_id=%d", chatID, feedID),
	}
}
