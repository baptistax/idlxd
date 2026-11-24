package app

import (
	"context"
	"errors"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/models"
)

func (d *profileDownload) posts(ctx context.Context, progress *Progress) error {
	return d.pages(ctx, progress, "posts", func(after string) ([]models.Media, models.PageInfo, error) {
		items, info, _, err := d.ig.FetchPostsPage(ctx, d.profile.Username, after)
		return items, info, err
	})
}

func (d *profileDownload) reels(ctx context.Context, progress *Progress) error {
	return d.pages(ctx, progress, "reels", func(after string) ([]models.Media, models.PageInfo, error) {
		return d.ig.FetchReelsPage(ctx, d.profile.Username, d.profile.UserID, after)
	})
}

func (d *profileDownload) pages(ctx context.Context, progress *Progress, category string, fetch func(string) ([]models.Media, models.PageInfo, error)) error {
	pages := instagram.NewPagination()
	after := ""
	var failures []error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		progress.loading(ctx)
		items, info, err := fetch(after)
		if err != nil {
			failures = append(failures, err)
			if !fault.OnlyPartial(err) {
				return errors.Join(failures...)
			}
		}
		if err := pages.Accept(ctx, instagram.MediaIDs(items), info); err != nil {
			return errors.Join(append(failures, err)...)
		}
		total, timelineReels := 0, 0
		for _, m := range items {
			count := max(1, len(m.CarouselMedia))
			if category == "posts" && m.IsReel() {
				timelineReels += count
			} else {
				total += count
			}
		}
		if timelineReels > 0 {
			d.reelProgress.addTotal(timelineReels)
		}
		progress.addTotal(total)
		for _, m := range items {
			for _, job := range timelineMediaJobs(m) {
				dir := category
				target := progress
				if category == "posts" && m.IsReel() {
					dir, target = "reels", &d.reelProgress
				}
				err := d.save(ctx, target, dir, job.media, job.idx)
				if err != nil {
					if target != progress {
						d.reelFailures = append(d.reelFailures, err)
						if ctx.Err() == nil && !fault.Global(err) {
							continue
						}
					}
					failures = append(failures, err)
					if ctx.Err() != nil || fault.Global(err) {
						return errors.Join(failures...)
					}
				}
			}
		}
		if !info.HasNextPage {
			break
		}
		after = info.EndCursor
	}
	return errors.Join(failures...)
}
