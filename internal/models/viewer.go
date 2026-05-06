package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Viewer struct {
	ID        bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID      string        `json:"uuid" bson:"uuid"`
	Name      string        `json:"name" bson:"name"`
	Color     string        `json:"color" bson:"color"`
	Pin       string        `json:"pin,omitempty" bson:"pin,omitempty"`
	UsePin    bool          `json:"use_pin" bson:"use_pin"`
	Status    string        `json:"status,omitempty" bson:"status,omitempty"`
	UserUUID  string        `json:"user_uuid" bson:"user_uuid"`
	CreatedAt time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt *time.Time    `json:"updated_at,omitempty" bson:"updated_at,omitempty"`
}

type ViewerResponse struct {
	ID        bson.ObjectID `json:"id"`
	UUID      string        `json:"uuid"`
	Name      string        `json:"name"`
	Color     string        `json:"color"`
	UsePin    bool          `json:"use_pin"`
	Status    string        `json:"status,omitempty"`
	UserUUID  string        `json:"user_uuid"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt *time.Time    `json:"updated_at,omitempty"`
}

func (v *Viewer) ToResponse() ViewerResponse {
	return ViewerResponse{
		ID:        v.ID,
		UUID:      v.UUID,
		Name:      v.Name,
		Color:     v.Color,
		UsePin:    v.UsePin,
		Status:    v.Status,
		UserUUID:  v.UserUUID,
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	}
}
