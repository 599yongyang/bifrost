package apimart

import "github.com/bytedance/sonic"

type APIMartImageRequest struct {
	Model     string   `json:"model"`
	Prompt    string   `json:"prompt"`
	N         *int     `json:"n,omitempty"`
	Size      *string  `json:"size,omitempty"`
	ImageURLs []string `json:"image_urls,omitempty"`
}

func (*APIMartImageRequest) GetExtraParams() map[string]interface{} { return nil }

type APIMartTaskSubmission struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

type APIMartSubmitResponse struct {
	Code  int                     `json:"code"`
	Data  []APIMartTaskSubmission `json:"data"`
	Error *APIMartErrorDetail     `json:"error,omitempty"`
}

type APIMartTaskResponse struct {
	Code  int                 `json:"code"`
	Data  *APIMartTask        `json:"data,omitempty"`
	Error *APIMartErrorDetail `json:"error,omitempty"`
}

type APIMartTask struct {
	ID            string              `json:"id"`
	Status        string              `json:"status"`
	Progress      int                 `json:"progress,omitempty"`
	Created       int64               `json:"created,omitempty"`
	Completed     int64               `json:"completed,omitempty"`
	ActualTime    float64             `json:"actual_time,omitempty"`
	EstimatedTime float64             `json:"estimated_time,omitempty"`
	Result        *APIMartTaskResult  `json:"result,omitempty"`
	Error         *APIMartErrorDetail `json:"error,omitempty"`
}

type APIMartTaskResult struct {
	Images []APIMartTaskImage `json:"images,omitempty"`
}

type APIMartTaskImage struct {
	URLs      []string `json:"url"`
	ExpiresAt int64    `json:"expires_at,omitempty"`
}

type APIMartErrorDetail struct {
	Code    interface{} `json:"code,omitempty"`
	Type    string      `json:"type,omitempty"`
	Message string      `json:"message,omitempty"`
}

// UnmarshalJSON also accepts task failures whose error value is a bare string.
func (e *APIMartErrorDetail) UnmarshalJSON(data []byte) error {
	var message string
	if err := sonic.Unmarshal(data, &message); err == nil {
		e.Message = message
		return nil
	}
	type alias APIMartErrorDetail
	var decoded alias
	if err := sonic.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = APIMartErrorDetail(decoded)
	return nil
}

type APIMartRawImageResponse struct {
	Code int                      `json:"code"`
	Data APIMartSanitizedTaskData `json:"data"`
}

type APIMartSanitizedTaskData struct {
	ID            string              `json:"id,omitempty"`
	Status        string              `json:"status,omitempty"`
	Progress      int                 `json:"progress,omitempty"`
	Created       int64               `json:"created,omitempty"`
	Completed     int64               `json:"completed,omitempty"`
	ActualTime    float64             `json:"actual_time,omitempty"`
	EstimatedTime float64             `json:"estimated_time,omitempty"`
	Result        *APIMartTaskResult  `json:"result,omitempty"`
	Error         *APIMartErrorDetail `json:"error,omitempty"`
}
