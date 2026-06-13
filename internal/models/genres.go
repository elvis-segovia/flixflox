package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Genre struct {
	ID        bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID      string        `json:"uuid" bson:"uuid"`
	Genre     string        `json:"genre" bson:"genre"`
	CreatedAt time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time     `json:"updated_at" bson:"updated_at"`
}

type GenreResponse struct {
	ID        bson.ObjectID `json:"id"`
	UUID      string        `json:"uuid"`
	Genre     string        `json:"genre"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func (g *Genre) ToResponse() GenreResponse {
	return GenreResponse{
		ID:        g.ID,
		UUID:      g.UUID,
		Genre:     g.Genre,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}
