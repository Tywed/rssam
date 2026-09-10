package httpserver

import (
	"net/http"

	"rssam/internal/storage"
)

type refreshAllResultDTO struct {
	Feeds     int `json:"feeds"`
	Queued    int `json:"queued"`
	Refreshed int `json:"refreshed"`
	Failed    int `json:"failed"`
	Inserted  int `json:"inserted"`
}

func (s *Server) handleRefreshAllFeeds(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	if s.refreshAll == nil {
		writeError(w, http.StatusServiceUnavailable, "job queue is not configured")
		return
	}

	feeds, queued, err := s.refreshAll.EnqueueRefreshAllPollJobs(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "enqueue refresh-all failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.audit.Record(r, storage.AuditFeedsRefreshAll, "", 0, map[string]any{"feeds": feeds, "queued": queued})
	writeJSON(w, http.StatusOK, listResponse[refreshAllResultDTO]{
		Data: refreshAllResultDTO{
			Feeds:  feeds,
			Queued: queued,
		},
		Total: 1,
	})
}
