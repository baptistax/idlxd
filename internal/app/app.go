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

	if err := ig.EnsureTokens(ctx); err != nil {
		printFooter(time.Since(startedAt), "failed due to fatal error")
		return err
	}

	profile, err := ig.FetchProfile(ctx, cfg.Username)
	if err != nil {
		printFooter(time.Since(startedAt), "failed due to fatal error")
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
	printKV("Profile ID", profile.UserID)
	printKV("Private", fmt.Sprintf("%t", profile.IsPrivate))
	if profile.PostsCountKnown {
		printKV("Posts", fmt.Sprintf("%d", profile.PostsCount))
	} else {
		printKV("Posts", "unknown")
	}
	fmt.Println()

	sectionErrors := runIndependentSections([]sectionRunner{
		func() error { return downloadPosts(ctx, ig, dl, pacer, safeUser, profile.Username) },
		func() error { return downloadReels(ctx, ig, dl, pacer, safeUser, profile.Username, profile.UserID) },
		func() error { return downloadStories(ctx, ig, dl, pacer, safeUser, profile.Username, profile.UserID) },
		func() error {
			return downloadHighlights(ctx, ig, dl, pacer, safeUser, profile.Username, profile.UserID)
		},
	})

	if sectionErrors == 0 {
		printFooter(time.Since(startedAt), "completed with no errors")
	} else {
		printFooter(time.Since(startedAt), "completed with section errors")
	}
	return nil
}

type sectionRunner func() error

func runIndependentSections(sections []sectionRunner) int {
	errors := 0
	for _, section := range sections {
		if section == nil {
			continue
		}
		if err := section(); err != nil {
			errors++
		}
	}
	return errors
}

func downloadPosts(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username string) error {
	printSectionHeader(1, 4, "Posts")
	var progress *Progress
	defer func() {
		if progress != nil {
			progress.Finish()
		}
	}()

	after := ""
	firstErr := error(nil)
	downloaded := 0
	failed := 0
	processItems := func(items []instagram.Media) {
		for _, m := range items {
			jobs := timelineMediaJobs(m)
			if len(jobs) > 0 && progress == nil {
				progress = NewProgress("POSTS")
				progress.Start()
			}
			if progress != nil {
				progress.AddTotal(len(jobs))
			}
			for _, job := range jobs {
				if err := downloadMedia(ctx, ig, dl, pacer, safeUser, username, "posts", job.media, job.idx); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					failed++
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
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		items, pageInfo, uid, err := ig.FetchPostsPage(ctx, username, after)
		if err != nil {
			printSectionError(err)
			printSectionSummary(downloaded, failed)
			return err
		}
		_ = uid
		processItems(items)

		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			break
		}
		after = pageInfo.EndCursor
		time.Sleep(250 * time.Millisecond)
	}

	if progress != nil {
		progress.Finish()
		progress = nil
	}
	printSectionSummary(downloaded, failed)
	printSectionError(firstErr)
	return firstErr
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

func downloadReels(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, userID string) error {
	printSectionHeader(2, 4, "Reels")
	after := ""
	firstErr := error(nil)
	downloaded := 0
	failed := 0
	var progress *Progress
	defer func() {
		if progress != nil {
			progress.Finish()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		items, pageInfo, err := ig.FetchReelsPage(ctx, username, userID, after)
		if err != nil {
			printSectionSummary(downloaded, failed)
			printSectionError(err)
			return err
		}
		if len(items) > 0 && progress == nil {
			progress = NewProgress("REELS")
			progress.Start()
		}
		if progress != nil {
			progress.AddTotal(len(items))
		}
		for _, item := range items {
			if err := downloadMedia(ctx, ig, dl, pacer, safeUser, username, "reels", item, 0); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				failed++
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

		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			break
		}
		after = pageInfo.EndCursor
		time.Sleep(250 * time.Millisecond)
	}

	if progress != nil {
		progress.Finish()
		progress = nil
	}
	printSectionSummary(downloaded, failed)
	printSectionError(firstErr)
	return firstErr
}

func downloadStories(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, userID string) error {
	printSectionHeader(3, 4, "Stories")
	items, err := ig.FetchStories(ctx, username, userID)
	if err != nil {
		printSectionSummary(0, 0)
		printSectionError(err)
		return err
	}
	downloaded, failed, firstErr := downloadMediaList(ctx, ig, dl, pacer, safeUser, username, "stories", "STORIES", items)
	printSectionSummary(downloaded, failed)
	printSectionError(firstErr)
	return firstErr
}

func downloadHighlights(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, userID string) error {
	printSectionHeader(4, 4, "Highlights")
	var progress *Progress
	defer func() {
		if progress != nil {
			progress.Finish()
		}
	}()

	hs, err := ig.FetchHighlightsTray(ctx, username, userID)
	if err != nil {
		printSectionSummary(0, 0)
		printSectionError(err)
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

		reels, pageInfo, err := ig.FetchHighlightsPage(ctx, username, reelIDs, after, 2)
		if err != nil {
			printSectionSummary(downloaded, 0)
			printSectionError(err)
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
				if err := downloadMedia(ctx, ig, dl, pacer, safeUser, username, subdir, item, i+1); err != nil {
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
	printSectionError(firstErr)
	return firstErr
}

func downloadMediaList(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, subdir, label string, items []instagram.Media) (int, int, error) {
	var progress *Progress
	if len(items) > 0 {
		progress = NewProgress(label)
		progress.Start()
		progress.AddTotal(len(items))
		defer progress.Finish()
	}

	firstErr := error(nil)
	downloaded := 0
	failed := 0
	for i, item := range items {
		idx := 0
		if len(items) > 1 {
			idx = i + 1
		}
		if err := downloadMedia(ctx, ig, dl, pacer, safeUser, username, subdir, item, idx); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed++
			if progress != nil {
				progress.IncFail()
			}
			continue
		}
		downloaded++
		if progress != nil {
			progress.IncOK()
		}
	}
	return downloaded, failed, firstErr
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

func downloadMedia(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, pacer *Pacer, safeUser, username, subdir string, m instagram.Media, idx int) error {
	id := m.PK
	if id == "" {
		id = m.ID
	}
	if id == "" {
		id = "media"
	}

	resolved, isVideo, err := resolveDownloadMedia(ctx, username, m, func(ctx context.Context, username, mediaPK, mediaCode string) (instagram.Media, error) {
		if ig == nil {
			return instagram.Media{}, fmt.Errorf("media %s is a video but has no downloadable video URL", id)
		}
		return ig.FetchMediaInfo(ctx, username, mediaPK, mediaCode)
	})
	if err != nil {
		return err
	}

	url := ""
	imageURLs := []string(nil)

	if isVideo {
		url = instagram.BestVideoURL(resolved)
		if url == "" {
			return fmt.Errorf("media %s is a video but has no downloadable video URL", id)
		}
	}
	if url == "" {
		imageURLs = instagram.BestImageURLs(resolved)
		if len(imageURLs) > 0 {
			url = imageURLs[0]
		}
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
	return hydrated, true, nil
}

func isVideoMedia(m instagram.Media) bool {
	return m.MediaType == 2 || m.ProductType == "clips" || m.ProductType == "reels"
}

func waitForDownloadTurn(ctx context.Context, pacer *Pacer) error {
	if pacer == nil {
		return nil
	}
	return pacer.Wait(ctx)
}
