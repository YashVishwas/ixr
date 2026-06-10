package schema

// AudioSpeechRequest is the canonical form of a POST /v1/audio/speech body.
type AudioSpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`                    // alloy | echo | fable | onyx | nova | shimmer
	ResponseFormat string  `json:"response_format,omitempty"` // mp3 | opus | aac | flac | wav | pcm
	Speed          float64 `json:"speed,omitempty"`           // 0.25–4.0
}

// TranscriptionRequest is the canonical form of a POST /v1/audio/transcriptions body.
// The audio file is sent as multipart form-data (field "file").
type TranscriptionRequest struct {
	Model       string  `json:"model"`
	Language    string  `json:"language,omitempty"`
	Prompt      string  `json:"prompt,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Format      string  `json:"response_format,omitempty"` // json | text | srt | verbose_json | vtt
}

// TranscriptionResponse is the canonical form of a /v1/audio/transcriptions response.
type TranscriptionResponse struct {
	Text string `json:"text"`
}
