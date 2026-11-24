package app

import (
	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/instagram"
)

func newProvider(cfg config.Config) (Provider, error) {
	name, err := config.ProviderName(cfg.Provider)
	if err != nil {
		return nil, err
	}
	if name == config.GraphProvider {
		return instagram.NewClient(instagram.Options{AccessToken: cfg.AccessToken, AccountID: cfg.AccountID})
	}
	return instagram.NewCookieProvider(config.ResolveCookiesPath(cfg.CookiesPath)), nil
}
