package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/utils"
)

func Run(ctx context.Context, cfg config.Config) error {
	startedAt := time.Now()
	cookiesPath := config.ResolveCookiesPath(cfg.CookiesPath)
	if _, err := os.Stat(cookiesPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cookies.txt not found (%s)", cookiesPath)
		}
		return fmt.Errorf("unable to access cookies file: %v", err)
	}

	if err := utils.EnsureDir(cfg.OutputRoot); err != nil {
		return fmt.Errorf("unable to create output directory (%s): %v", cfg.OutputRoot, err)
	}

	ig, err := instagram.NewClient(instagram.Options{
		CookiesPath: cookiesPath,
		UserAgent:   cfg.UserAgent,
	})
	if err != nil {
		return err
	}

	dl := downloader.New(downloader.Options{
		OutputDir: cfg.OutputRoot,
		UserAgent: cfg.UserAgent,
		Referer:   "https://www.instagram.com/",
	})
	pacer := NewPacer(150*time.Millisecond, 350*time.Millisecond)
	pacer.Start()
	defer pacer.Stop()

	profile, err := ig.FetchProfile(ctx, cfg.Username)
	if err != nil {
		return err
	}

	safeUser := utils.SanitizePathSegment(profile.Username)
	if safeUser == "" {
		return errors.New("invalid username")
	}

	userRoot := filepath.Join(cfg.OutputRoot, safeUser)
	if err := utils.EnsureDir(userRoot); err != nil {
		return fmt.Errorf("unable to create user output directory (%s): %v", userRoot, err)
	}

	printBanner()
	printKV("Target", profile.Username)
	printKV("Output", userRoot)
	if profile.UserID != "" {
		printKV("Profile ID", profile.UserID)
	}
	fmt.Println()

	firstErr := error(nil)
	userID := profile.UserID

	timelineUserID, err := downloadTimeline(ctx, ig, dl, pacer, safeUser, profile.Username)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	if userID == "" {
		userID = timelineUserID
	}

	if userID != "" {
		if err := downloadHighlights(ctx, ig, dl, pacer, safeUser, profile.Username, userID); err != nil && firstErr == nil {
			firstErr = err
		}
	} else if firstErr == nil {
		firstErr = errors.New("failed to resolve profile id")
	}

	printFooter(time.Since(startedAt), firstErr == nil)
	return firstErr
}

func downloadTimeline(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username string) (string, error) {
	printSectionHeader(1, 2, "Posts / Reels")
	var progress *Progress
	defer func() {
		if progress != nil {
			progress.Finish()
		}
	}()

	after := ""
	userID := ""
	firstErr := error(nil)
	downloaded := 0

	for {
		select {
		case <-ctx.Done():
			return userID, ctx.Err()
		default:
		}

		items, pageInfo, uid, err := ig.FetchPostsPage(ctx, username, after)
		if err != nil {
			return userID, err
		}
		if userID == "" && uid != "" {
			userID = uid
		}

		for _, m := range items {
			jobs := timelineMediaJobs(m)
			if len(jobs) > 0 && progress == nil {
				progress = NewProgress("POSTS / REELS")
				progress.Start()
			}
			if progress != nil {
				progress.AddTotal(len(jobs))
			}
			for _, job := range jobs {
				if err := downloadMedia(ctx, dl, pacer, safeUser, "posts", job.media, job.idx); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					if progress != nil {
						progress.IncFail()
					}
				} else {
					downloaded++
					if progress != nil {
						progress.IncOK()
					}
				}
			}
		}

		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			break
		}
		after = pageInfo.EndCursor
		time.Sleep(250 * time.Millisecond)
	}

	failed := 0
	if progress != nil {
		failed = progress.Failed()
		progress.Finish()
		progress = nil
	}
	printSectionSummary(downloaded, failed)
	return userID, firstErr
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

func downloadHighlights(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, userID string) error {
	printSectionHeader(2, 2, "Highlights")
	var progress *Progress
	defer func() {
		if progress != nil {
			progress.Finish()
		}
	}()

	hs, err := ig.FetchHighlightsTray(ctx, username, userID)
	if err != nil {
		return err
	}
	if len(hs) == 0 {
		printSectionSummary(0, 0)
		return nil
	}

	reelIDs := make([]string, 0, len(hs))
	for _, h := range hs {
		reelIDs = append(reelIDs, h.ID)
	}
	idToTitle := highlightDirNames(hs)

	after := ""
	firstErr := error(nil)
	downloaded := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		reels, pageInfo, err := ig.FetchHighlightsPage(ctx, username, reelIDs, after, 10)
		if err != nil {
			return err
		}

		for _, r := range reels {
			title := idToTitle[r.ID]
			if title == "" {
				title = "highlight"
			}
			subdir := filepath.Join("highlights", title)
			if len(r.Items) > 0 && progress == nil {
				progress = NewProgress("HIGHLIGHTS")
				progress.Start()
			}
			if progress != nil {
				progress.AddTotal(len(r.Items))
			}
			for i, item := range r.Items {
				if err := downloadMedia(ctx, dl, pacer, safeUser, subdir, item, i+1); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					if progress != nil {
						progress.IncFail()
					}
				} else {
					downloaded++
					if progress != nil {
						progress.IncOK()
					}
				}
			}
		}

		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			break
		}
		after = pageInfo.EndCursor
		time.Sleep(250 * time.Millisecond)
	}

	failed := 0
	if progress != nil {
		failed = progress.Failed()
		progress.Finish()
		progress = nil
	}
	printSectionSummary(downloaded, failed)
	return firstErr
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
	if name == "" {
		return "highlight"
	}
	return name
}

func downloadMedia(ctx context.Context, dl *downloader.Downloader, pacer *Pacer, safeUser, subdir string, m instagram.Media, idx int) error {
	id := m.PK
	if id == "" {
		id = m.ID
	}
	if id == "" {
		id = "media"
	}

	url := ""
	isVideo := false
	imageURLs := []string(nil)

	if m.MediaType == 2 || m.ProductType == "clips" || m.ProductType == "reels" {
		url = instagram.BestVideoURL(m)
		isVideo = true
	}
	if url == "" {
		imageURLs = instagram.BestImageURLs(m)
		if len(imageURLs) > 0 {
			url = imageURLs[0]
		}
		isVideo = false
	}
	if url == "" {
		return fmt.Errorf("media %s has no downloadable URL", id)
	}

	ext := ""
	if isVideo {
		ext = utils.ExtFromURL(url)
		if ext == "" {
			ext = ".mp4"
		}
	} else {
		// Force a stable and widely supported image format.
		ext = ".jpg"
	}

	ts := "unknown"
	if m.TakenAt > 0 {
		ts = time.Unix(m.TakenAt, 0).UTC().Format("20060102_150405")
	}

	part := ""
	if idx > 0 {
		part = fmt.Sprintf("_%02d", idx)
	}

	name := fmt.Sprintf("%s_%s%s%s", ts, id, part, ext)
	rel := filepath.Join(safeUser, subdir, name)

	if isVideo {
		if err := waitForDownloadTurn(ctx, pacer); err != nil {
			return err
		}
		if _, err := dl.DownloadToFile(ctx, url, rel); err != nil {
			return fmt.Errorf("failed to download %s: %v", name, err)
		}
		return nil
	}

	lastErr := error(nil)
	if len(imageURLs) == 0 {
		imageURLs = []string{url}
	}
	for _, u := range imageURLs {
		if err := waitForDownloadTurn(ctx, pacer); err != nil {
			lastErr = err
			break
		}
		if _, err := dl.DownloadImageAsJPEG(ctx, u, rel); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("failed to download %s: %v", name, lastErr)
	}
	return nil
}

func waitForDownloadTurn(ctx context.Context, pacer *Pacer) error {
	if pacer == nil {
		return nil
	}
	return pacer.Wait(ctx)
}
