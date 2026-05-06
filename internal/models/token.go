package models

import "time"

type BlacklistedToken struct {
	JTI       string    `json:"jti" bson:"jti"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	ExpiresAt time.Time `json:"expires_at" bson:"expires_at"`
}
