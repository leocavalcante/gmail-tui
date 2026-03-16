package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	credentialsFile = "credentials.json"
	tokenFileName   = ".gmail-tui-token.json"
)

func newGmailService() (*gmail.Service, error) {
	ctx := context.Background()

	config, err := loadOAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load OAuth config: %w", err)
	}

	client, err := getAuthenticatedClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get authenticated client: %w", err)
	}

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gmail service: %w", err)
	}

	return srv, nil
}

func loadOAuthConfig() (*oauth2.Config, error) {
	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(data, gmail.MailGoogleComScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse credentials: %w", err)
	}

	return config, nil
}

func getAuthenticatedClient(ctx context.Context, config *oauth2.Config) (*http.Client, error) {
	tokenPath, err := getTokenPath()
	if err != nil {
		return nil, fmt.Errorf("failed to determine token path: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		token, err = performOAuthFlow(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("OAuth flow failed: %w", err)
		}
		if err := saveToken(tokenPath, token); err != nil {
			return nil, fmt.Errorf("failed to save token: %w", err)
		}
	}

	ts := config.TokenSource(ctx, token)
	refreshedToken, err := ts.Token()
	if err != nil {
		token, err = performOAuthFlow(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("OAuth flow failed after refresh error: %w", err)
		}
		if err := saveToken(tokenPath, token); err != nil {
			return nil, fmt.Errorf("failed to save token: %w", err)
		}
		return config.Client(ctx, token), nil
	}

	if refreshedToken.AccessToken != token.AccessToken ||
		refreshedToken.RefreshToken != token.RefreshToken ||
		!refreshedToken.Expiry.Equal(token.Expiry) {
		_ = saveToken(tokenPath, refreshedToken)
	}

	return oauth2.NewClient(ctx, ts), nil
}

func getTokenPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, tokenFileName), nil
}

func loadToken(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	token := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(token); err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}
	return token, nil
}

func saveToken(path string, token *oauth2.Token) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(token); err != nil {
		return fmt.Errorf("failed to encode token: %w", err)
	}
	return nil
}

func performOAuthFlow(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	fmt.Printf("\nAuthorization required. Please visit:\n%s\n\n", authURL)
	fmt.Print("Enter authorization code: ")

	var code string
	if _, err := fmt.Scanln(&code); err != nil {
		return nil, fmt.Errorf("failed to read authorization code: %w", err)
	}

	token, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	return token, nil
}
