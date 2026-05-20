package schema

import "time"

// TokenUsagePoint is one bucket of observed or forecast token usage.
type TokenUsagePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Tokens    int       `json:"tokens"`
}

// TokenForecastRequest describes a token-usage forecast query.
type TokenForecastRequest struct {
	UserID         string        `json:"user_id"`
	Model          string        `json:"model,omitempty"`
	Window         time.Duration `json:"-"`
	Horizon        time.Duration `json:"-"`
	Bucket         time.Duration `json:"-"`
	FreeTokenLimit int           `json:"free_token_limit,omitempty"`
}

// TokenForecastResponse summarizes observed and projected token consumption.
type TokenForecastResponse struct {
	UserID                   string            `json:"user_id"`
	Model                    string            `json:"model,omitempty"`
	WindowHours              float64           `json:"window_hours"`
	HorizonHours             float64           `json:"horizon_hours"`
	BucketMinutes            float64           `json:"bucket_minutes"`
	ConsumedTokens           int               `json:"consumed_tokens"`
	FreeTokenLimit           int               `json:"free_token_limit,omitempty"`
	FreeTokensRemaining      int               `json:"free_tokens_remaining,omitempty"`
	CurrentRateTokensPerHour float64           `json:"current_rate_tokens_per_hour"`
	ProjectedTokens          int               `json:"projected_tokens"`
	ProjectedTotalTokens     int               `json:"projected_total_tokens"`
	ProjectedOverLimit       bool              `json:"projected_over_limit"`
	Forecast                 []TokenUsagePoint `json:"forecast"`
}

// ForecastJobStatus is the lifecycle state for an asynchronous forecast job.
type ForecastJobStatus string

const (
	ForecastJobQueued    ForecastJobStatus = "queued"
	ForecastJobRunning   ForecastJobStatus = "running"
	ForecastJobSucceeded ForecastJobStatus = "succeeded"
	ForecastJobFailed    ForecastJobStatus = "failed"
)

// TokenForecastJob is the persisted state of an asynchronous forecast request.
type TokenForecastJob struct {
	ID        string                 `json:"id"`
	Status    ForecastJobStatus      `json:"status"`
	Request   TokenForecastJobParams `json:"request"`
	Result    *TokenForecastResponse `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// TokenForecastJobParams is the JSON-friendly form of TokenForecastRequest.
type TokenForecastJobParams struct {
	UserID         string  `json:"user_id"`
	Model          string  `json:"model,omitempty"`
	WindowHours    float64 `json:"window_hours,omitempty"`
	HorizonHours   float64 `json:"horizon_hours,omitempty"`
	BucketMinutes  float64 `json:"bucket_minutes,omitempty"`
	FreeTokenLimit int     `json:"free_token_limit,omitempty"`
}
