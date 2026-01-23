package instagram

import (
	"bufio"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func LoadNetscapeCookiesIntoJar(path string, jar http.CookieJar) error {
	if jar == nil {
		return errors.New("cookie jar inválido")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	byHost := map[string][]*http.Cookie{}

	s := bufio.NewScanner(f)
	for s.Scan() {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			continue
		}

		httpOnly := false
		if strings.HasPrefix(raw, "#HttpOnly_") {
			httpOnly = true
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "#HttpOnly_"))
		} else if strings.HasPrefix(raw, "#") {
			continue
		}

		parts := strings.Fields(raw)
		if len(parts) < 7 {
			continue
		}

		domain := strings.TrimSpace(parts[0])
		pathPart := strings.TrimSpace(parts[2])
		secureStr := strings.TrimSpace(parts[3])
		expiryStr := strings.TrimSpace(parts[4])
		name := strings.TrimSpace(parts[5])
		value := strings.TrimSpace(strings.Join(parts[6:], " "))

		if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = strings.TrimSuffix(strings.TrimPrefix(value, "\""), "\"")
		}

		secure := secureStr == "TRUE" || secureStr == "true" || secureStr == "1"

		var expires time.Time
		if expiryStr != "" {
			if unix, err := strconv.ParseInt(expiryStr, 10, 64); err == nil && unix > 0 {
				expires = time.Unix(unix, 0)
			}
		}

		host := strings.TrimPrefix(domain, ".")
		if host == "" || name == "" {
			continue
		}

		if pathPart == "" {
			pathPart = "/"
		}

		ck := &http.Cookie{
			Name:     name,
			Value:    value,
			Path:     pathPart,
			Domain:   host,
			Secure:   secure,
			Expires:  expires,
			HttpOnly: httpOnly,
		}

		byHost[host] = append(byHost[host], ck)
	}

	if err := s.Err(); err != nil {
		return err
	}

	for host, cookies := range byHost {
		setFor := map[string]struct{}{}
		setFor[host] = struct{}{}
		if !strings.HasPrefix(host, "www.") {
			setFor["www."+host] = struct{}{}
		}
		if !strings.HasPrefix(host, "i.") {
			setFor["i."+host] = struct{}{}
		}

		for h := range setFor {
			httpsURL := &url.URL{Scheme: "https", Host: h, Path: "/"}
			httpURL := &url.URL{Scheme: "http", Host: h, Path: "/"}
			jar.SetCookies(httpsURL, cookies)
			jar.SetCookies(httpURL, cookies)
		}
	}

	return nil
}
