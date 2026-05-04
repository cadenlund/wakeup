package user

import "github.com/google/uuid"

// CreateParams is the input to Queries.Create. Validation (length, charset,
// uniqueness pre-checks) is the service layer's job; this struct only
// carries shape.
type CreateParams struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	Email        string
	PasswordHash string
}

// UpdateParams is the input to Queries.Update. Pointer fields use NULL
// semantics: nil means "don't change," non-nil means "set to this value."
//
// Bio + StatusEmoji: empty string is a valid stored value (the UI treats
// "" the same as "no bio displayed"). Pass nil to leave unchanged; pass
// a *"" to actively blank the field.
type UpdateParams struct {
	ID          uuid.UUID
	DisplayName *string
	AvatarURL   *string
	ColorScheme *string
	Bio         *string
	StatusEmoji *string
}
