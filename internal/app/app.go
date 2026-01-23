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
    if _, err := os.Stat(cfg.CookiesPath); err != nil {
        if os.IsNotExist(err) {
            return fmt.Errorf("cookies.txt não encontrado (%s)", cfg.CookiesPath)
        }
        return fmt.Errorf("não foi possível acessar o arquivo de cookies: %v", err)
    }

    if err := utils.EnsureDir(cfg.OutputRoot); err != nil {
        return fmt.Errorf("não foi possível criar a pasta de saída (%s): %v", cfg.OutputRoot, err)
    }

    ig, err := instagram.NewClient(instagram.Options{
        CookiesPath: cfg.CookiesPath,
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

    profile, err := ig.FetchProfile(ctx, cfg.Username)
    if err != nil {
        return err
    }

    safeUser := utils.SanitizePathSegment(profile.Username)
    if safeUser == "" {
        return errors.New("usuário inválido")
    }

    userRoot := filepath.Join(cfg.OutputRoot, safeUser)
    if err := utils.EnsureDir(userRoot); err != nil {
        return fmt.Errorf("não foi possível criar a pasta do usuário (%s): %v", userRoot, err)
    }

    fmt.Printf("Alvo: %s\n", profile.Username)
    fmt.Printf("Saída: %s\n", userRoot)

    firstErr := error(nil)

    userID, err := downloadTimeline(ctx, ig, dl, safeUser, profile.Username)
    if err != nil && firstErr == nil {
        firstErr = err
    }

    if userID != "" {
        if err := downloadHighlights(ctx, ig, dl, safeUser, profile.Username, userID); err != nil && firstErr == nil {
            firstErr = err
        }
    } else if firstErr == nil {
        firstErr = errors.New("não foi possível obter o id do perfil")
    }

    return firstErr
}

func downloadTimeline(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, safeUser, username string) (string, error) {
    fmt.Println("Baixando posts/reels...")
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
            if err := downloadMedia(ctx, dl, safeUser, "posts", m, 0); err != nil && firstErr == nil {
                firstErr = err
            } else if err == nil {
                downloaded++
            }

            if len(m.CarouselMedia) > 0 {
                for i, cm := range m.CarouselMedia {
                    if err := downloadMedia(ctx, dl, safeUser, "posts", cm, i+1); err != nil && firstErr == nil {
                        firstErr = err
                    } else if err == nil {
                        downloaded++
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

    fmt.Printf("Posts/reels: %d arquivos\n", downloaded)
    return userID, firstErr
}

func downloadHighlights(ctx context.Context, ig *instagram.Client, dl *downloader.Downloader, safeUser, username, userID string) error {
    fmt.Println("Baixando destaques...")
    hs, err := ig.FetchHighlightsTray(ctx, username, userID)
    if err != nil {
        return err
    }
    if len(hs) == 0 {
        fmt.Println("Destaques: 0")
        return nil
    }

    idToTitle := map[string]string{}
    reelIDs := make([]string, 0, len(hs))
    for _, h := range hs {
        reelIDs = append(reelIDs, h.ID)
        title := utils.SanitizePathSegment(h.Title)
        if title == "" {
            title = "highlight"
        }
        idToTitle[h.ID] = title
    }

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
            for i, item := range r.Items {
                if err := downloadMedia(ctx, dl, safeUser, subdir, item, i+1); err != nil && firstErr == nil {
                    firstErr = err
                } else if err == nil {
                    downloaded++
                }
            }
        }

        if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
            break
        }
        after = pageInfo.EndCursor
        time.Sleep(250 * time.Millisecond)
    }

    fmt.Printf("Destaques: %d arquivos\n", downloaded)
    return firstErr
}

func downloadMedia(ctx context.Context, dl *downloader.Downloader, safeUser, subdir string, m instagram.Media, idx int) error {
    url := ""
    isVideo := false

    if m.MediaType == 2 || m.ProductType == "clips" || m.ProductType == "reels" {
        url = instagram.BestVideoURL(m)
        isVideo = true
    }
    if url == "" {
        url = instagram.BestImageURL(m)
        isVideo = false
    }
    if url == "" {
        return nil
    }

    ext := utils.ExtFromURL(url)
    if ext == "" {
        if isVideo {
            ext = ".mp4"
        } else {
            ext = ".jpg"
        }
    }

    ts := "unknown"
    if m.TakenAt > 0 {
        ts = time.Unix(m.TakenAt, 0).UTC().Format("20060102_150405")
    }
    id := m.PK
    if id == "" {
        id = m.ID
    }
    if id == "" {
        id = "media"
    }

    part := ""
    if idx > 0 {
        part = fmt.Sprintf("_%02d", idx)
    }

    name := fmt.Sprintf("%s_%s%s%s", ts, id, part, ext)
    rel := filepath.Join(safeUser, subdir, name)

    if _, err := dl.DownloadToFile(ctx, url, rel); err != nil {
        return fmt.Errorf("falha ao baixar %s: %v", name, err)
    }
    return nil
}
