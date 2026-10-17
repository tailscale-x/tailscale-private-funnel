package tailscale

import (
	"context"
	"time"

	"golang.org/x/oauth2"
)

type clientTokenSource struct{ client Client }

func (s clientTokenSource) Token() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.client.retrieveOAuthToken(ctx)
}

// CachedOAuth uses the standard concurrency-safe OAuth2 reuse source. Tokens
// remain in memory and are renewed before the server-provided expiry.
func (c Client) CachedOAuth() Client {
	if c.oauthSource == nil {
		c.oauthSource = oauth2.ReuseTokenSource(nil, clientTokenSource{client: c})
	}
	return c
}
func (c Client) oauthToken(ctx context.Context) (string, error) {
	var token *oauth2.Token
	var err error
	if c.oauthSource != nil {
		token, err = c.oauthSource.Token()
	} else {
		token, err = c.retrieveOAuthToken(ctx)
	}
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}
