package model

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// trackingParams are query parameters that identify a click or a campaign,
// never the content. Feeds and share buttons rotate them (yclid/fbclid per
// request, utm_campaign per newsletter issue), so the same article would get
// a new dedup hash each time; they also leak the reader's path to the site.
// Matching is on the lower-cased key. Migration 0037 rewrote the rows stored
// before 0.1.11 with the list as it was then; additions here apply to new rows.
var trackingParams = map[string]struct{}{
	// Google Ads / Analytics
	"gclid": {}, "dclid": {}, "gbraid": {}, "wbraid": {}, "gclsrc": {}, "srsltid": {}, "_ga": {}, "_gl": {},
	// Yandex
	"yclid": {}, "ysclid": {}, "_openstat": {},
	// Facebook / Instagram / TikTok / Twitter / LinkedIn / Microsoft
	"fbclid": {}, "fb_action_ids": {}, "fb_action_types": {}, "fb_ref": {}, "fb_source": {}, "fb_comment_id": {},
	"igshid": {}, "ttclid": {}, "twclid": {}, "ref_src": {}, "ref_url": {}, "li_fat_id": {}, "msclkid": {},
	// Mail: Mailchimp, HubSpot, Marketo, Adobe, Beehiiv, Vero, Olytics, Wicked Reports, Branch.io
	"mc_cid": {}, "mc_eid": {}, "mc_tc": {},
	"_hsenc": {}, "_hsmi": {}, "__hssc": {}, "__hstc": {}, "__hsfp": {}, "hsctatracking": {}, "hsa_cam": {},
	"mkt_tok": {}, "sc_cid": {}, "_bhlid": {}, "vero_id": {}, "vero_conv": {},
	"oly_anon_id": {}, "oly_enc_id": {}, "rb_clickid": {}, "wickedid": {},
	"_branch_match_id": {}, "_branch_referrer": {},
}

// trackingPrefixes: utm_* (Google), mtm_*/pk_* (Matomo), itm_*/hmb_* (internal
// campaign trackers used by several publishers).
var trackingPrefixes = []string{"utm_", "mtm_", "pk_", "itm_", "hmb_"}

func isTrackingParam(rawKey string) bool {
	key := strings.ToLower(rawKey)
	for _, p := range trackingPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	_, ok := trackingParams[key]
	return ok
}

// StripTrackingParams removes tracking parameters from a raw query string,
// keeping the order and the raw encoding of everything else so that a query
// without trackers is returned byte-for-byte unchanged (existing hashes stay
// valid). When something is removed, empty segments ("a&&b") are dropped too.
// Returns the new query and whether anything was removed.
func StripTrackingParams(rawQuery string) (string, bool) {
	if rawQuery == "" {
		return rawQuery, false
	}
	found := false
	for seg := range strings.SplitSeq(rawQuery, "&") {
		key, _, _ := strings.Cut(seg, "=")
		if isTrackingParam(key) {
			found = true
			break
		}
	}
	if !found {
		return rawQuery, false
	}
	kept := make([]string, 0, 4)
	for seg := range strings.SplitSeq(rawQuery, "&") {
		if seg == "" {
			continue
		}
		key, _, _ := strings.Cut(seg, "=")
		if isTrackingParam(key) {
			continue
		}
		kept = append(kept, seg)
	}
	return strings.Join(kept, "&"), true
}

// NormalizeURL is the canonical form used for dedup hashes and for the
// stored entry URL: no fragment, lower-cased scheme and host, "/" for an
// empty path, no tracking parameters (http/https only). Anything that does
// not parse as an absolute URL is returned as is.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}

	u.Fragment = ""
	u.RawFragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "http" || u.Scheme == "https" {
		if q, stripped := StripTrackingParams(u.RawQuery); stripped {
			u.RawQuery = q
			u.ForceQuery = false
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String()
}

func DedupHashFromURL(raw string) string {
	n := NormalizeURL(raw)
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])
}

// DedupHashFromString hashes an arbitrary stable key (e.g. owner_id_post_id).
func DedupHashFromString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
