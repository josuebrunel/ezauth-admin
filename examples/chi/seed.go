package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/service"
)

type demoUser struct{ Email, FirstName, LastName, Username string }

// demoUsers gives the panel something to show on first start.
var demoUsers = []demoUser{
	{"alice@example.com", "Alice", "Johnson", "alice"},
	{"bob@example.com", "Bob", "Smith", "bob"},
	{"carol@example.com", "Carol", "Davis", "carol"},
	{"dave@example.com", "Dave", "Wilson", "dave"},
}

// seed makes sure the admin role, the admin user and the demo users exist.
// It is idempotent: existing accounts are never touched, so their passwords
// are only ever logged once, at creation. This is a demo convenience, not a
// pattern for a real deployment.
func seed(ctx context.Context, auth *ezauth.EzAuth, adminEmail string) error {
	role := auth.Config.AdminRole

	if _, err := auth.Repo.RoleGetByName(ctx, role); err != nil {
		if _, err := auth.RoleCreate(ctx, role, "Full administrative access"); err != nil {
			return fmt.Errorf("create %s role: %w", role, err)
		}
	}

	adminUser, err := ensureUser(ctx, auth, demoUser{Email: adminEmail})
	if err != nil {
		return err
	}
	if err := auth.UserRoleGrant(ctx, adminUser.ID, role); err != nil {
		return fmt.Errorf("grant %s role: %w", role, err)
	}

	for _, du := range demoUsers {
		if _, err := ensureUser(ctx, auth, du); err != nil {
			return err
		}
	}
	return nil
}

type userRef struct{ ID string }

func ensureUser(ctx context.Context, auth *ezauth.EzAuth, du demoUser) (userRef, error) {
	if u, err := auth.Repo.UserGetByEmail(ctx, du.Email); err == nil {
		return userRef{ID: u.ID}, nil
	}
	password, err := randomPassword()
	if err != nil {
		return userRef{}, fmt.Errorf("generate password for %s: %w", du.Email, err)
	}
	u, err := auth.Service.UserCreate(ctx, &service.RequestBasicAuth{
		Email:     du.Email,
		Password:  password,
		FirstName: du.FirstName,
		LastName:  du.LastName,
		Username:  du.Username,
	})
	if err != nil {
		return userRef{}, fmt.Errorf("create user %s: %w", du.Email, err)
	}
	slog.Info("created user, save this password: it is shown only once", "email", du.Email, "password", password)
	return userRef{ID: u.ID}, nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
