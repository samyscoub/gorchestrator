package http

type jsonErr struct {
	Code int    `json:"code"`
	Text string `json:"message"`
}
