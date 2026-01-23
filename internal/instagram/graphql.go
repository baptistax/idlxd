package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	httpClient *http.Client
	userAgent  string
	lsd        string
	fbDtsg     string
	dsUserID   string
}

type Options struct {
	CookiesPath string
	UserAgent   string
	Timeout     time.Duration
}

func NewClient(opts Options) (*Client, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(opts.CookiesPath) == "" {
		return nil, errors.New("caminho do cookies.txt vazio")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if err := LoadNetscapeCookiesIntoJar(opts.CookiesPath, jar); err != nil {
		return nil, err
	}

	c := &http.Client{
		Timeout: opts.Timeout,
		Jar:     jar,
	}

	cl := &Client{
		httpClient: c,
		userAgent:  opts.UserAgent,
	}
	cl.dsUserID = cl.cookieValue("ds_user_id")
	return cl, nil
}

func (c *Client) FetchProfile(ctx context.Context, username string) (Profile, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Profile{}, errors.New("uso: idl <usuario>")
	}
	return Profile{Username: username}, nil
}

func (c *Client) EnsureTokens(ctx context.Context) error {
	if c.lsd != "" && c.fbDtsg != "" {
		return nil
	}

	if c.cookieValue("sessionid") == "" {
		return errors.New("cookies.txt não contém sessionid (exporte os cookies do Instagram logado; Cookie-Editor costuma gerar linha #HttpOnly_... sessionid)")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseWWW+"/", nil)
	if err != nil {
		return err
	}
	c.applyCommonHeaders(req, baseWWW+"/")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("falha ao acessar o Instagram: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Instagram retornou %s", resp.Status)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	html := string(b)

	lsd := firstMatch(html, lsdPatterns)
	fb := firstMatch(html, dtsgPatterns)

	if lsd == "" || fb == "" {
		return errors.New("não foi possível obter tokens da sessão (Instagram pode ter exigido verificação; abra o Instagram no navegador, confirme o login e gere o cookies.txt novamente)")
	}

	c.lsd = lsd
	c.fbDtsg = fb
	return nil
}

func (c *Client) GraphQL(ctx context.Context, referer, friendlyName, docID string, variables any, out any) error {
	if err := c.EnsureTokens(ctx); err != nil {
		return err
	}

	v, err := json.Marshal(variables)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("fb_api_caller_class", "RelayModern")
	form.Set("fb_api_req_friendly_name", friendlyName)
	form.Set("server_timestamps", "true")
	form.Set("doc_id", docID)
	form.Set("variables", string(v))
	form.Set("lsd", c.lsd)
	form.Set("fb_dtsg", c.fbDtsg)
	form.Set("jazoest", jazoestFromDtsg(c.fbDtsg))
	form.Set("__a", "1")
	form.Set("__d", "www")
	form.Set("__user", "0")
	if c.dsUserID != "" {
		form.Set("av", c.dsUserID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	if referer == "" {
		referer = baseWWW + "/"
	}

	c.applyCommonHeaders(req, referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-FB-LSD", c.lsd)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", asbdID)

	if csrf := c.cookieValue("csrftoken"); csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("falha na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Instagram retornou %s", resp.Status)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("resposta inesperada do Instagram")
	}
	return nil
}

func (c *Client) applyCommonHeaders(req *http.Request, referer string) {
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", baseWWW)
	req.Header.Set("Referer", referer)
}

func (c *Client) cookieValue(name string) string {
	if c.httpClient == nil || c.httpClient.Jar == nil {
		return ""
	}
	u, _ := url.Parse(baseWWW + "/")
	for _, ck := range c.httpClient.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func jazoestFromDtsg(s string) string {
	sum := 0
	for _, r := range s {
		sum += int(r)
	}
	return "2" + fmt.Sprintf("%d", sum)
}

var lsdPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"LSD",\[\],\{"token":"([^"]+)"\}`),
	regexp.MustCompile(`"lsd"\s*:\s*"\s*([^"]+)\s*"`),
	regexp.MustCompile(`name="lsd"\s+value="([^"]+)"`),
}

var dtsgPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"DTSGInitialData",\[\],\{"token":"([^"]+)"\}`),
	regexp.MustCompile(`"fb_dtsg"\s*:\s*"\s*([^"]+)\s*"`),
	regexp.MustCompile(`name="fb_dtsg"\s+value="([^"]+)"`),
}

func firstMatch(s string, patterns []*regexp.Regexp) string {
	for _, p := range patterns {
		m := p.FindStringSubmatch(s)
		if len(m) == 2 {
			return m[1]
		}
	}
	return ""
}
