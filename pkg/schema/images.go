package schema

// ImageRequest is the canonical form of a POST /v1/images/generations body.
type ImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`              // number of images (default 1)
	Size           string `json:"size,omitempty"`           // "1024x1024"
	Quality        string `json:"quality,omitempty"`        // "standard" | "hd"
	Style          string `json:"style,omitempty"`          // "vivid" | "natural"
	ResponseFormat string `json:"response_format,omitempty"` // "url" | "b64_json"
	User           string `json:"user,omitempty"`
}

// ImageData holds one generated image.
type ImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageResponse is the canonical form of a /v1/images/generations response.
type ImageResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
}
