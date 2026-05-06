package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Address struct {
	Street  string `json:"street,omitempty" bson:"street,omitempty"`
	City    string `json:"city,omitempty" bson:"city,omitempty"`
	Country string `json:"country,omitempty" bson:"country,omitempty"`
}

type Profile struct {
	FirstName   string  `json:"first_name,omitempty" bson:"first_name,omitempty"`
	LastName    string  `json:"last_name,omitempty" bson:"last_name,omitempty"`
	DateOfBirth string  `json:"date_of_birth,omitempty" bson:"date_of_birth,omitempty"`
	Address     Address `json:"address,omitempty" bson:"address,omitempty"`
}

type User struct {
	ID         bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID       string        `json:"uuid" bson:"uuid"`
	Username   string        `json:"username" bson:"username"`
	Password   string        `json:"password,omitempty" bson:"password"`
	Email      string        `json:"email" bson:"email"`
	Avatar     string        `json:"avatar,omitempty" bson:"avatar,omitempty"`
	Role       string        `json:"role,omitempty" bson:"role,omitempty"`
	Privileges []string      `json:"privileges" bson:"privileges"`
	Profile    *Profile      `json:"profile,omitempty" bson:"profile,omitempty"`
	CreatedAt  time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt  *time.Time    `json:"updated_at,omitempty" bson:"updated_at,omitempty"`
}

type UserResponse struct {
	ID         bson.ObjectID `json:"id"`
	UUID       string        `json:"uuid"`
	Username   string        `json:"username"`
	Email      string        `json:"email"`
	Avatar     string        `json:"avatar,omitempty"`
	Role       string        `json:"role,omitempty"`
	Privileges []string      `json:"privileges"`
	Profile    *Profile      `json:"profile,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  *time.Time    `json:"updated_at,omitempty"`
}

func (u *User) ToResponse() UserResponse {
	return UserResponse{
		ID:         u.ID,
		UUID:       u.UUID,
		Username:   u.Username,
		Email:      u.Email,
		Avatar:     u.Avatar,
		Role:       u.Role,
		Privileges: u.Privileges,
		Profile:    u.Profile,
		CreatedAt:  u.CreatedAt,
		UpdatedAt:  u.UpdatedAt,
	}
}
