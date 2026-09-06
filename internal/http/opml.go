package httpserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"rssam/internal/http/middleware"
	"rssam/internal/opml"
	"rssam/internal/reader"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type importErrorDTO struct {
	FeedURL string `json:"feed_url,omitempty"`
	Title   string `json:"title,omitempty"`
	Reason  string `json:"reason"`
}

type importReportDTO struct {
	CategoriesCreated int              `json:"categories_created"`
	FeedsCreated      int              `json:"feeds_created"`
	FeedsSkipped      int              `json:"feeds_skipped"`
	Errors            []importErrorDTO `json:"errors"`
}

func (s *Server) handleImportFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	data, err := readOPMLBody(r)
	if err != nil {
		if middleware.IsBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	doc, err := opml.ParseBytes(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if r.URL.Query().Get("async") == "1" {
		jobID := s.startAsyncOPMLImport(r, doc, p.UserID)
		writeJSON(w, http.StatusAccepted, listResponse[map[string]string]{
			Data:  map[string]string{"job_id": jobID, "status": "pending"},
			Total: 1,
		})
		return
	}

	report := s.importOPML(r, doc, p.UserID, "")
	writeJSON(w, http.StatusOK, listResponse[importReportDTO]{Data: report, Total: 1})
}

func (s *Server) importOPMLFromBytes(r *http.Request, userID int64, data []byte) importReportDTO {
	doc, err := opml.ParseBytes(data)
	if err != nil {
		return importReportDTO{Errors: []importErrorDTO{{Reason: err.Error()}}}
	}
	return s.importOPML(r, doc, userID, "")
}

func (s *Server) exportOPMLForUser(w http.ResponseWriter, ctx context.Context, userID int64) error {
	if s.categories == nil || s.feeds == nil {
		return errors.New("storage is not configured")
	}
	categories, _, err := s.categories.ListCategories(ctx, userID, 10000, 0)
	if err != nil {
		return err
	}
	feeds, _, err := s.feeds.ListFeeds(ctx, userID, 10000, 0)
	if err != nil {
		return err
	}

	exportCats := make([]opml.ExportCategory, 0, len(categories))
	for _, c := range categories {
		exportCats = append(exportCats, opml.ExportCategory{ID: c.ID, Title: c.Title})
	}
	exportFeeds := make([]opml.ExportFeed, 0, len(feeds))
	for _, f := range feeds {
		exportFeeds = append(exportFeeds, opml.ExportFeed{
			Title:      f.Title,
			FeedURL:    f.FeedURL,
			CategoryID: f.CategoryID,
		})
	}

	xml, err := opml.Generate(opml.ExportInput{
		Title:      "rssam subscriptions",
		Categories: exportCats,
		Feeds:      exportFeeds,
	})
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(xml)
	return nil
}

func (s *Server) handleExportFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.exportOPMLForUser(w, r.Context(), p.UserID); err != nil {
		s.log.ErrorContext(r.Context(), "export feeds failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func readOPMLBody(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	if ct != "" {
		mediaType, params, err := mime.ParseMediaType(ct)
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				return nil, err
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				return nil, errors.New("multipart form must include file field")
			}
			defer file.Close()
			return io.ReadAll(file)
		}
		_ = params
	}
	return io.ReadAll(r.Body)
}

func (s *Server) importOPML(r *http.Request, doc *opml.Document, userID int64, jobID string) importReportDTO {
	ctx := r.Context()
	report := importReportDTO{Errors: make([]importErrorDTO, 0)}

	existingFeeds, _, _ := s.feeds.ListFeeds(ctx, userID, 10000, 0)
	knownFeedURLs := make(map[string]struct{}, len(existingFeeds))
	for _, f := range existingFeeds {
		knownFeedURLs[strings.ToLower(strings.TrimSpace(f.FeedURL))] = struct{}{}
	}

	existingCats, _, _ := s.categories.ListCategories(ctx, userID, 10000, 0)
	categoryByTitle := make(map[string]int64, len(existingCats))
	for _, c := range existingCats {
		key := strings.ToLower(strings.TrimSpace(c.Title))
		if key != "" {
			categoryByTitle[key] = c.ID
		}
	}

	entries := doc.CollectFeeds()
	limit := s.maxImportFeeds
	if limit <= 0 {
		limit = 500
	}

	seenInBatch := make(map[string]struct{}, len(entries))

	for i, entry := range entries {
		if i >= limit {
			report.Errors = append(report.Errors, importErrorDTO{
				Reason: "import limit exceeded",
			})
			break
		}
		if jobID != "" && s.importJobs != nil {
			processed := i + 1
			s.importJobs.update(jobID, func(j *importJob) {
				j.Processed = processed
			})
		}

		feedURL := strings.TrimSpace(entry.FeedURL)
		normURL := strings.ToLower(feedURL)
		if feedURL == "" {
			report.Errors = append(report.Errors, importErrorDTO{
				Title:  entry.Title,
				Reason: "feed_url is empty",
			})
			continue
		}
		if _, dup := seenInBatch[normURL]; dup {
			report.FeedsSkipped++
			report.Errors = append(report.Errors, importErrorDTO{
				FeedURL: feedURL,
				Title:   entry.Title,
				Reason:  "duplicate in import file",
			})
			continue
		}
		seenInBatch[normURL] = struct{}{}

		if _, exists := knownFeedURLs[normURL]; exists {
			report.FeedsSkipped++
			report.Errors = append(report.Errors, importErrorDTO{
				FeedURL: feedURL,
				Title:   entry.Title,
				Reason:  "feed_url already exists",
			})
			continue
		}

		if err := validateFeedURL(feedURL, s.ssrfGuard); err != nil {
			report.Errors = append(report.Errors, importErrorDTO{
				FeedURL: feedURL,
				Title:   entry.Title,
				Reason:  err.Error(),
			})
			continue
		}

		var categoryID *int64
		catTitle := strings.TrimSpace(entry.CategoryTitle)
		if catTitle != "" {
			key := strings.ToLower(catTitle)
			id, ok := categoryByTitle[key]
			if !ok {
				category, err := s.categories.CreateCategory(ctx, userID, catTitle, "")
				if err != nil {
					report.Errors = append(report.Errors, importErrorDTO{
						Title:  catTitle,
						Reason: "create category: " + err.Error(),
					})
					continue
				}
				report.CategoriesCreated++
				id = category.ID
				categoryByTitle[key] = id
			}
			categoryID = &id
		}

		feed, err := s.feeds.CreateFeed(ctx, userID, storage.CreateFeedParams{
			FeedType:        reader.DetectFeedTypeFromURL(feedURL),
			FeedURL:         feedURL,
			Title:           strings.TrimSpace(entry.Title),
			CategoryID:      categoryID,
			IntervalMinutes: defaultIntervalMinutes,
		})
		if err != nil {
			if errors.Is(err, storage.ErrDuplicateFeedURL) {
				report.FeedsSkipped++
				knownFeedURLs[normURL] = struct{}{}
				report.Errors = append(report.Errors, importErrorDTO{
					FeedURL: feedURL,
					Title:   entry.Title,
					Reason:  "feed_url already exists",
				})
				continue
			}
			report.Errors = append(report.Errors, importErrorDTO{
				FeedURL: feedURL,
				Title:   entry.Title,
				Reason:  err.Error(),
			})
			continue
		}

		report.FeedsCreated++
		knownFeedURLs[normURL] = struct{}{}
		_ = feed
	}

	return report
}

func validateFeedURL(feedURL string, guard *ssrf.Guard) error {
	return reader.ValidateFeedURL(feedURL, guard)
}
