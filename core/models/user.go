package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/null/v3"
)

const (
	// NilUserID is the id 0 considered as nil user id
	NilUserID = UserID(0)
)

// UserID is our type for user ids, which can be null
type UserID int

func (i *UserID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i UserID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *UserID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i UserID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }

type UserUUID string
type UserRole string

const (
	UserRoleAdministrator UserRole = "A"
	UserRoleEditor        UserRole = "E"
	UserRoleAgent         UserRole = "T"
)

// User is our type for a user asset
type User struct {
	ID_        UserID   `json:"id"`
	UUID_      UserUUID `json:"uuid"`
	Email_     string   `json:"email"`
	FirstName_ string   `json:"first_name"`
	LastName_  string   `json:"last_name"`
	Role_      UserRole `json:"role_code"`
	Team_      *Team    `json:"team"`
}

func (u *User) ID() UserID     { return u.ID_ }
func (u *User) UUID() UserUUID { return u.UUID_ }
func (u *User) Email() string  { return u.Email_ }
func (u *User) Role() UserRole { return u.Role_ }

// Name returns the name
func (u *User) Name() string {
	names := make([]string, 0, 2)
	if u.FirstName_ != "" {
		names = append(names, u.FirstName_)
	}
	if u.LastName_ != "" {
		names = append(names, u.LastName_)
	}
	return strings.Join(names, " ")
}

// Team returns the user's ticketing team if any
func (u *User) Team() *Team {
	return u.Team_
}

var _ assets.User = (*User)(nil)

const sqlSelectUsersByOrg = `
SELECT ROW_TO_JSON(r) FROM (
           SELECT u.id, u.uuid, u.email, u.first_name, u.last_name, m.role_code, row_to_json(team_struct) AS team
             FROM orgs_orgmembership m
       INNER JOIN users_user u ON u.id = m.user_id
LEFT JOIN LATERAL (SELECT id, uuid, name FROM tickets_team WHERE tickets_team.id = m.team_id) AS team_struct ON True
            WHERE m.org_id = $1 AND u.is_active = TRUE
         ORDER BY u.email ASC
) r;`

// loadUsers loads all the users for the passed in org
func loadUsers(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.User, error) {
	rows, err := db.QueryContext(ctx, sqlSelectUsersByOrg, orgID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying users for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.User { return &User{} })
}

func GetSystemUserID(ctx context.Context, db *sql.DB) (UserID, error) {
	var id UserID
	err := db.QueryRowContext(ctx, "SELECT id FROM users_user WHERE email = 'system'").Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("error getting system user id: %w", err)
	}
	return id, nil
}
