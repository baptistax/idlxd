package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/models"
	"github.com/baptistax/idl/internal/utils"
)

// Provider is the actual boundary: only normalized internal models cross it.
type Provider interface {
	Capabilities() map[string]models.Capability
	FetchProfile(context.Context, string) (models.Profile, error)
	FetchPostsPage(context.Context, string, string) ([]models.Media, models.PageInfo, string, error)
	FetchReelsPage(context.Context, string, string, string) ([]models.Media, models.PageInfo, error)
	FetchStories(context.Context, string, string) ([]models.Media, error)
	FetchHighlightsTray(context.Context, string, string) ([]models.Highlight, error)
	FetchHighlightsPage(context.Context, string, []string, string, int) ([]models.HighlightMedia, models.PageInfo, error)
	FetchMediaInfo(context.Context, string, string, string) (models.Media, error)
}

func Run(ctx context.Context, cfg config.Config) error {
	if cfg.Status {
		return status(ctx, cfg, stdoutTerminal())
	}
	ig, err := newProvider(cfg)
	return runDownload(ctx, cfg, ig, err, stdoutTerminal())
}

func RunWithProvider(ctx context.Context, cfg config.Config, ig Provider) error {
	return runDownload(ctx, cfg, ig, nil, stdoutTerminal())
}

func runDownload(ctx context.Context, cfg config.Config, ig Provider, setupErr error, ui terminal) (result error) {
	start := time.Now()
	username, err := models.NormalizeUsername(cfg.Username)
	if err != nil {
		return err
	}
	safeUser := utils.SanitizePathSegment(username)
	userRoot := filepath.Join(cfg.OutputRoot, safeUser)
	ui.banner("Instagram Downloader")
	fmt.Fprintf(ui.out, "Target: %s\nOutput: %s\n\n", username, userRoot)
	started := false
	var files fileCounts
	defer func() {
		fmt.Fprintln(ui.out)
		switch {
		case result == nil:
			fmt.Fprint(ui.out, "Download completed. ")
		case !started:
			fmt.Fprintln(ui.out, ui.styled(userMessage(result), "31"))
			fmt.Fprintln(ui.out, "Download not started.")
		case files.saved+files.existing > 0:
			fmt.Fprint(ui.out, "Partial completion. ")
		default:
			fmt.Fprint(ui.out, "Download incomplete. ")
		}
		if started {
			noun := "files"
			if files.saved == 1 {
				noun = "file"
			}
			fmt.Fprintf(ui.out, "%d %s saved.", files.saved, noun)
			if files.existing > 0 {
				fmt.Fprintf(ui.out, " %d existing.", files.existing)
			}
			if files.repeated > 0 {
				fmt.Fprintf(ui.out, " %d repeated.", files.repeated)
			}
			if files.failed > 0 {
				fmt.Fprintf(ui.out, " %d failed.", files.failed)
			}
			fmt.Fprintln(ui.out)
		}
		ui.finished(start)
	}()

	if setupErr != nil {
		ui.check("Authentication", setupErr)
		return setupErr
	}
	if cookies, ok := ig.(*instagram.CookieProvider); ok {
		err := ui.wait(ctx, "Session file", cookies.CheckSessionFile)
		ui.check("Session file", err)
		if err != nil {
			return err
		}
	}
	var profile models.Profile
	err = ui.wait(ctx, "Authentication", func() error {
		var err error
		profile, err = ig.FetchProfile(ctx, username)
		if err == nil && (profile.Username != username || profile.UserID == "") {
			return fault.New(fault.Permission, "profile", "provider returned an unverified account")
		}
		return err
	})
	if err != nil {
		switch {
		case errors.Is(err, instagram.ErrTargetProfileMissing):
			ui.check("Authentication", nil)
			ui.check("Profile", err)
		case fault.Is(err, fault.Authentication):
			ui.check("Authentication", err)
		default:
			ui.warning("Authentication: not verified")
			ui.check("Profile", err)
		}
		return err
	}
	ui.check("Authentication", nil)
	ui.check("Profile", nil)
	if err := utils.EnsureDir(userRoot); err != nil {
		ui.check("Local storage", err)
		return &fault.Error{Kind: fault.Storage, Op: "output", Cause: err}
	}
	dlOpts := downloader.Options{OutputDir: cfg.OutputRoot, UserAgent: cfg.UserAgent}
	if cookies, ok := ig.(*instagram.CookieProvider); ok {
		dlOpts.HTTPClient = cookies.MediaClient()
	}
	dl := downloader.New(dlOpts)
	pacer := NewPacer(150*time.Millisecond, 350*time.Millisecond)
	pacer.Start()
	defer pacer.Stop()

	fmt.Fprintln(ui.out)
	download := profileDownload{ig: ig, dl: dl, pacer: pacer, safeUser: safeUser, profile: profile}
	download.existing, err = indexExisting(ctx, userRoot)
	if err != nil {
		return &fault.Error{Kind: fault.Storage, Op: "output", Cause: err}
	}
	started = true
	files, err = download.runSections(ctx, ui)
	return err
}

type profileDownload struct {
	ig           Provider
	dl           *downloader.Downloader
	pacer        *Pacer
	safeUser     string
	profile      models.Profile
	existing     existingMedia
	attempts     map[string]bool // Media ID to video flag, including failed attempts.
	reelProgress Progress
	reelFailures []error
}

type fileCounts struct {
	saved, existing, repeated, failed int
}

func (d *profileDownload) runSections(ctx context.Context, ui terminal) (fileCounts, error) {
	caps := d.ig.Capabilities()
	var failures []error
	var files fileCounts
	var stopped error
	for _, section := range []struct {
		label string
		run   func(context.Context, *Progress) error
	}{
		{"Posts", d.posts},
		{"Reels", d.reels},
		{"Stories", d.stories},
		{"Highlights", d.highlights},
	} {
		name := strings.ToLower(section.label)
		progress := &Progress{ui: ui, label: section.label}
		if name == "reels" {
			progress = &d.reelProgress
			progress.ui, progress.label = ui, section.label
		}
		var err error
		skip := ""
		switch {
		case stopped != nil:
			skip = "not checked"
		case ctx.Err() != nil:
			skip, err = "not checked", ctx.Err()
		case !caps[name].Supported:
			skip, err = "unavailable", fault.New(fault.Unsupported, name, caps[name].Reason)
		default:
			err = section.run(ctx, progress)
		}
		if name == "reels" {
			err = errors.Join(append(d.reelFailures, err)...)
		}
		progress.finish(err, skip)
		files.saved += progress.saved
		files.existing += progress.existing
		files.repeated += progress.repeated
		files.failed += progress.failed
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			if ctx.Err() != nil || fault.Global(err) {
				stopped = err
			}
		}
	}
	return files, errors.Join(failures...)
}

type timelineMediaJob struct {
	media instagram.Media
	idx   int
}

func timelineMediaJobs(m instagram.Media) []timelineMediaJob {
	if len(m.CarouselMedia) == 0 {
		return []timelineMediaJob{{media: m}}
	}

	jobs := make([]timelineMediaJob, 0, len(m.CarouselMedia))
	for i, cm := range m.CarouselMedia {
		jobs = append(jobs, timelineMediaJob{
			media: cm,
			idx:   i + 1,
		})
	}
	return jobs
}

func (d *profileDownload) stories(ctx context.Context, progress *Progress) error {
	progress.loading(ctx)
	items, err := d.ig.FetchStories(ctx, d.profile.Username, d.profile.UserID)
	if err != nil && !fault.OnlyPartial(err) {
		return err
	}
	progress.addTotal(len(items))
	var failures []error
	for i, item := range items {
		idx := 0
		if len(items) > 1 {
			idx = i + 1
		}
		itemErr := d.save(ctx, progress, "stories", item, idx)
		if itemErr != nil {
			failures = append(failures, itemErr)
			if ctx.Err() != nil || fault.Global(itemErr) {
				break
			}
		}
	}
	return errors.Join(append(failures, err)...)
}

func (d *profileDownload) highlights(ctx context.Context, progress *Progress) error {
	progress.loading(ctx)
	hs, err := d.ig.FetchHighlightsTray(ctx, d.profile.Username, d.profile.UserID)
	if err != nil && !fault.OnlyPartial(err) || len(hs) == 0 {
		return err
	}

	reelIDs := make([]string, 0, len(hs))
	for _, h := range hs {
		reelIDs = append(reelIDs, h.ID)
	}
	idToTitle := highlightDirNames(hs)

	after := ""
	pages := instagram.NewPagination()
	var failures []error
	if err != nil {
		failures = append(failures, err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}

		progress.loading(ctx)
		reels, pageInfo, err := d.ig.FetchHighlightsPage(ctx, d.profile.Username, reelIDs, after, 2)
		if err != nil {
			failures = append(failures, err)
			if !fault.OnlyPartial(err) {
				return errors.Join(failures...)
			}
		}
		ids := make([]string, 0, len(reels))
		for _, reel := range reels {
			if _, ok := idToTitle[reel.ID]; !ok {
				return errors.Join(append(failures, fault.New(fault.Permission, "highlights", "collection was not in the verified tray"))...)
			}
			ids = append(ids, reel.ID)
			items := instagram.NewPagination()
			if err := items.Accept(ctx, instagram.MediaIDs(reel.Items), models.PageInfo{}); err != nil {
				return errors.Join(append(failures, err)...)
			}
		}
		if err := pages.Accept(ctx, ids, pageInfo); err != nil {
			return errors.Join(append(failures, err)...)
		}

		total := 0
		for _, reel := range reels {
			total += len(reel.Items)
		}
		progress.addTotal(total)
		for _, r := range reels {
			title := idToTitle[r.ID]
			if title == "" {
				title = "highlight"
			}
			subdir := filepath.Join("highlights", title)
			for i, item := range r.Items {
				err := d.save(ctx, progress, subdir, item, i+1)
				if err != nil {
					failures = append(failures, err)
					if ctx.Err() != nil || fault.Global(err) {
						return errors.Join(failures...)
					}
				}
			}
		}
		if !pageInfo.HasNextPage {
			break
		}
		after = pageInfo.EndCursor
	}
	return errors.Join(failures...)
}

func highlightDirNames(hs []instagram.Highlight) map[string]string {
	baseCounts := make(map[string]int, len(hs))
	for _, h := range hs {
		baseCounts[highlightDirBaseName(h.Title)]++
	}

	dirs := make(map[string]string, len(hs))
	used := make(map[string]struct{}, len(hs))
	for _, h := range hs {
		base := highlightDirBaseName(h.Title)
		name := base
		if baseCounts[base] > 1 {
			name = fmt.Sprintf("%s_%s", base, utils.SanitizePathSegment(h.ID))
		}
		for suffix := 2; ; suffix++ {
			if _, exists := used[name]; !exists {
				break
			}
			name = fmt.Sprintf("%s_%02d", base, suffix)
		}
		used[name] = struct{}{}
		dirs[h.ID] = name
	}
	return dirs
}

func highlightDirBaseName(title string) string {
	name := utils.SanitizePathSegment(title)
	if name == "" || name == "unknown" {
		return "highlight"
	}
	return name
}

func (d *profileDownload) save(ctx context.Context, progress *Progress, subdir string, m instagram.Media, idx int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := m.PK
	if id == "" {
		id = m.ID
	}
	video := isVideoMedia(m)
	if id == "" {
		return progress.record(id, fault.New(fault.Schema, "media", "missing media identity"))
	}
	if priorVideo, ok := d.attempts[id]; ok {
		if priorVideo != video {
			return progress.record(id, fault.New(fault.Schema, "media", "conflicting types for the same media"))
		}
		progress.repeated++
		progress.update()
		return nil
	}
	if d.attempts == nil {
		d.attempts = make(map[string]bool)
	}
	d.attempts[id] = video
	present, err := d.existing.valid(ctx, id, video)
	if present {
		progress.existing++
		progress.update()
		return nil
	}
	if err == nil {
		err = downloadMedia(ctx, d.ig, d.dl, d.pacer, d.safeUser, d.profile.Username, subdir, m, idx)
	}
	return progress.record(id, err)
}

func downloadMedia(ctx context.Context, ig Provider, dl *downloader.Downloader, pacer *Pacer, safeUser, username, subdir string, m instagram.Media, idx int) error {
	id := m.PK
	if id == "" {
		id = m.ID
	}
	if id == "" {
		id = "media"
	}

	var hydrate mediaHydrator
	if ig != nil {
		hydrate = ig.FetchMediaInfo
	}
	resolved, isVideo, err := resolveDownloadMedia(ctx, username, m, hydrate)
	if err != nil {
		return err
	}

	url := instagram.BestImageURL(resolved)
	ext := ".jpg"
	if isVideo {
		url = instagram.BestVideoURL(resolved)
		ext = ".mp4"
	}
	if url == "" {
		return fault.New(fault.Unavailable, "media", "no complete representation supplied")
	}
	ts := "unknown"
	taken := m.TakenAt
	if taken == 0 {
		taken = resolved.TakenAt
	}
	if taken > 0 {
		ts = time.Unix(taken, 0).UTC().Format("20060102_150405")
	}

	part := ""
	if idx > 0 {
		part = fmt.Sprintf("_%02d", idx)
	}

	name := fmt.Sprintf("%s_%s%s%s", ts, utils.SanitizePathSegment(id), part, ext)
	rel := filepath.Join(safeUser, subdir, name)

	if err := waitForDownloadTurn(ctx, pacer); err != nil {
		return err
	}
	_, err = dl.DownloadMedia(ctx, url, rel, isVideo)
	if err != nil && !fault.Global(err) {
		if fault.Is(err, fault.Unsupported) {
			return &fault.Error{Kind: fault.Unavailable, Op: "download", Cause: err}
		}
		return &fault.Error{Kind: fault.Download, Op: "download", Cause: err}
	}
	return err
}

type mediaHydrator func(context.Context, string, string, string) (instagram.Media, error)

func resolveDownloadMedia(ctx context.Context, username string, m instagram.Media, hydrate mediaHydrator) (instagram.Media, bool, error) {
	if !isVideoMedia(m) {
		return m, false, nil
	}

	if instagram.BestVideoURL(m) != "" {
		return m, true, nil
	}
	if hydrate == nil || m.PK == "" {
		return m, true, nil
	}

	hydrated, err := hydrate(ctx, username, m.PK, m.Code)
	if err != nil {
		return m, true, err
	}
	if hydrated.PK != m.PK || m.User.PK != "" && hydrated.User.PK != m.User.PK || m.Code != "" && hydrated.Code != "" && hydrated.Code != m.Code {
		return m, true, fault.New(fault.Permission, "media hydration", "returned media does not match verified item")
	}
	if !isVideoMedia(hydrated) {
		return m, true, fault.New(fault.Schema, "media hydration", "selected video has a conflicting media type")
	}
	return hydrated, true, nil
}

func isVideoMedia(m instagram.Media) bool {
	return m.MediaType == 2 || m.IsReel()
}

func waitForDownloadTurn(ctx context.Context, pacer *Pacer) error {
	if pacer == nil {
		return nil
	}
	return pacer.Wait(ctx)
}
