package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Cast struct {
	ID          bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID        string        `json:"uuid" bson:"uuid"`
	Name        string        `json:"name" bson:"name"`
	BirthDate   string        `json:"birth_date" bson:"birth_date,omitempty"`
	Nationality string        `json:"nationality,omitempty" bson:"nationality,omitempty"`
	Gender      string        `json:"gender,omitempty" bson:"gender,omitempty"`
	CreatedAt   time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at" bson:"updated_at"`
}

type CastResponse struct {
	ID          bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID        string        `json:"uuid" bson:"uuid"`
	Name        string        `json:"name" bson:"name"`
	BirthDate   string        `json:"birth_date" bson:"birth_date,omitempty"`
	Nationality string        `json:"nationality,omitempty" bson:"nationality,omitempty"`
	Gender      string        `json:"gender,omitempty" bson:"gender,omitempty"`
	CreatedAt   time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at" bson:"updated_at"`
}

func (c *Cast) ToResponse() CastResponse {
	return CastResponse{
		ID:          c.ID,
		UUID:        c.UUID,
		Name:        c.Name,
		BirthDate:   c.BirthDate,
		Nationality: c.Nationality,
		Gender:      c.Gender,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}
