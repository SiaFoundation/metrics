package api

import "time"

type UserCountResponse struct {
	Hosts   int64 `json:"hosts"`
	Renters int64 `json:"renters"`

	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
