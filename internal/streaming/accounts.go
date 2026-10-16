package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Account describes a debrid key: whether it works and the plan behind it.
type Account struct {
	Provider string `json:"provider"`
	OK       bool   `json:"ok"`
	User     string `json:"user,omitempty"`
	Premium  bool   `json:"premium"`
	Expires  string `json:"expires,omitempty"` // RFC 3339
	Error    string `json:"error,omitempty"`
}

// CheckAccount verifies a debrid API key.
func CheckAccount(ctx context.Context, provider, key string) Account {
	a := Account{Provider: provider}
	if strings.TrimSpace(key) == "" {
		a.Error = "no API key"
		return a
	}
	var err error
	switch provider {
	case "torbox":
		err = torboxAccount(ctx, key, &a)
	case "alldebrid":
		err = allDebridAccount(ctx, key, &a)
	case "realdebrid":
		err = realDebridAccount(ctx, key, &a)
	default:
		a.Error = "unknown provider"
		return a
	}
	if err != nil {
		a.OK, a.Error = false, err.Error()
	}
	return a
}

func getJSON(ctx context.Context, g *guard, endpoint, url, key string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	b, status, err := apiCall(ctx, g, apiClient, endpoint, req)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return accountError("the API key was rejected")
		}
		return err
	}
	return json.Unmarshal(b, out)
}

type accountError string

func (e accountError) Error() string { return string(e) }

func torboxAccount(ctx context.Context, key string, a *Account) error {
	var r struct {
		Success bool   `json:"success"`
		Detail  string `json:"detail"`
		Data    struct {
			Email            string `json:"email"`
			Plan             int    `json:"plan"`
			PremiumExpiresAt string `json:"premium_expires_at"`
		} `json:"data"`
	}
	if err := getJSON(ctx, torboxGuard, "user", torboxAPI+"/user/me", key, &r); err != nil {
		return err
	}
	if !r.Success {
		return accountError(r.Detail)
	}
	a.OK, a.User, a.Premium = true, r.Data.Email, r.Data.Plan > 0
	if t, err := time.Parse(time.RFC3339, r.Data.PremiumExpiresAt); err == nil {
		a.Expires = t.UTC().Format(time.RFC3339)
	}
	return nil
}

func allDebridAccount(ctx context.Context, key string, a *Account) error {
	var r struct {
		Status string `json:"status"`
		Data   struct {
			User struct {
				Username     string `json:"username"`
				IsPremium    bool   `json:"isPremium"`
				PremiumUntil int64  `json:"premiumUntil"`
			} `json:"user"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := getJSON(ctx, allDebridGuard, "user", allDebridAPI+"/v4/user?agent=wrentv", key, &r); err != nil {
		return err
	}
	if r.Status != "success" {
		return accountError(r.Error.Message)
	}
	u := r.Data.User
	a.OK, a.User, a.Premium = true, u.Username, u.IsPremium
	if u.PremiumUntil > 0 {
		a.Expires = time.Unix(u.PremiumUntil, 0).UTC().Format(time.RFC3339)
	}
	return nil
}

func realDebridAccount(ctx context.Context, key string, a *Account) error {
	var r struct {
		Username   string `json:"username"`
		Type       string `json:"type"`
		Expiration string `json:"expiration"`
	}
	if err := getJSON(ctx, realDebridGuard, "/user", realDebridAPI+"/user", key, &r); err != nil {
		return err
	}
	a.OK, a.User, a.Premium = true, r.Username, r.Type == "premium"
	if t, err := time.Parse(time.RFC3339, r.Expiration); err == nil {
		a.Expires = t.UTC().Format(time.RFC3339)
	}
	return nil
}
