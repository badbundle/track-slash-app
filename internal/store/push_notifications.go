package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PushNotificationCategory string

const (
	PushNotificationMentions       PushNotificationCategory = "mentions"
	PushNotificationAssignments    PushNotificationCategory = "assignments"
	PushNotificationComments       PushNotificationCategory = "comments"
	PushNotificationStatusChanges  PushNotificationCategory = "status_changes"
	PushNotificationDueDateChanges PushNotificationCategory = "due_date_changes"
)

var pushMentionPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])@([A-Za-z0-9][A-Za-z0-9_-]{2,31})`)

type UpsertPushSubscriptionParams struct {
	UserID     uuid.UUID
	Endpoint   string
	P256DH     string
	AuthSecret string
	UserAgent  string
}

type PushNotificationEvent struct {
	ChangelogID  uuid.UUID
	AttemptCount int
	CreatedAt    time.Time
}

type PushNotificationDelivery struct {
	ID             uuid.UUID
	ChangelogID    uuid.UUID
	SubscriptionID uuid.UUID
	UserID         uuid.UUID
	ProjectID      uuid.UUID
	IssueID        uuid.UUID
	Category       PushNotificationCategory
	Endpoint       string
	P256DH         string
	AuthSecret     string
	AttemptCount   int
}

type PushNotificationPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

func DefaultPushNotificationPreferences() model.PushNotificationPreferences {
	return model.PushNotificationPreferences{Mentions: true, Assignments: true}
}

func (s *Store) GetPushNotificationPreferences(ctx context.Context, userID uuid.UUID) (model.PushNotificationPreferences, error) {
	var out model.PushNotificationPreferences
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(p.mentions, true), COALESCE(p.assignments, true),
		       COALESCE(p.comments, false), COALESCE(p.status_changes, false),
		       COALESCE(p.due_date_changes, false)
		FROM users u
		LEFT JOIN user_push_notification_preferences p ON p.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID).Scan(&out.Mentions, &out.Assignments, &out.Comments, &out.StatusChanges, &out.DueDateChanges)
	if err != nil {
		if isNoRows(err) {
			return model.PushNotificationPreferences{}, ErrNotFound
		}
		return model.PushNotificationPreferences{}, err
	}
	return out, nil
}

func (s *Store) UpdatePushNotificationPreferences(ctx context.Context, userID uuid.UUID, p model.PushNotificationPreferences) (model.PushNotificationPreferences, error) {
	var out model.PushNotificationPreferences
	err := s.db.QueryRow(ctx, `
		INSERT INTO user_push_notification_preferences (
			user_id, mentions, assignments, comments, status_changes, due_date_changes
		)
		SELECT id, $2, $3, $4, $5, $6
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		ON CONFLICT (user_id) DO UPDATE
		SET mentions = EXCLUDED.mentions,
		    assignments = EXCLUDED.assignments,
		    comments = EXCLUDED.comments,
		    status_changes = EXCLUDED.status_changes,
		    due_date_changes = EXCLUDED.due_date_changes,
		    updated_at = now()
		RETURNING mentions, assignments, comments, status_changes, due_date_changes
	`, userID, p.Mentions, p.Assignments, p.Comments, p.StatusChanges, p.DueDateChanges).
		Scan(&out.Mentions, &out.Assignments, &out.Comments, &out.StatusChanges, &out.DueDateChanges)
	if err != nil {
		if isNoRows(err) {
			return model.PushNotificationPreferences{}, ErrNotFound
		}
		return model.PushNotificationPreferences{}, err
	}
	return out, nil
}

func (s *Store) UpsertPushSubscription(ctx context.Context, p UpsertPushSubscriptionParams) (model.PushSubscription, error) {
	endpoint, p256dh, authSecret, userAgent, err := normalizePushSubscription(p)
	if err != nil {
		return model.PushSubscription{}, err
	}
	var out model.PushSubscription
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var lastSuccessAt, disabledAt sql.NullTime
		err := tx.QueryRow(ctx, `
			INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth_secret, user_agent)
			SELECT id, $2, $3, $4, $5
			FROM users
			WHERE id = $1 AND deleted_at IS NULL
			ON CONFLICT (endpoint) DO UPDATE
			SET user_id = EXCLUDED.user_id,
			    p256dh = EXCLUDED.p256dh,
			    auth_secret = EXCLUDED.auth_secret,
			    user_agent = EXCLUDED.user_agent,
			    failure_count = 0,
			    disabled_at = NULL,
			    updated_at = now()
			RETURNING id, user_id, endpoint, p256dh, auth_secret, user_agent, failure_count,
			          last_success_at, disabled_at, created_at, updated_at
		`, p.UserID, endpoint, p256dh, authSecret, userAgent).Scan(
			&out.ID, &out.UserID, &out.Endpoint, &out.P256DH, &out.AuthSecret, &out.UserAgent,
			&out.FailureCount, &lastSuccessAt, &disabledAt, &out.CreatedAt, &out.UpdatedAt,
		)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if lastSuccessAt.Valid {
			out.LastSuccessAt = &lastSuccessAt.Time
		}
		if disabledAt.Valid {
			out.DisabledAt = &disabledAt.Time
		}
		_, err = tx.Exec(ctx, `
			UPDATE push_notification_deliveries
			SET suppressed_at = now(), locked_at = NULL, last_error = 'browser subscription replaced'
			WHERE subscription_id = $1
			  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, out.ID)
		return err
	})
	if err != nil {
		return model.PushSubscription{}, err
	}
	return out, nil
}

func normalizePushSubscription(p UpsertPushSubscriptionParams) (string, string, string, string, error) {
	endpoint := strings.TrimSpace(p.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || len(endpoint) > 4096 {
		return "", "", "", "", fmt.Errorf("push endpoint must be an HTTPS URL: %w", ErrConflict)
	}
	if !pushEndpointHostAllowed(parsed.Hostname()) {
		return "", "", "", "", fmt.Errorf("push endpoint must be a public address: %w", ErrConflict)
	}
	p256dh := strings.TrimSpace(p.P256DH)
	authSecret := strings.TrimSpace(p.AuthSecret)
	publicKey, publicErr := base64.RawURLEncoding.DecodeString(p256dh)
	authKey, authErr := base64.RawURLEncoding.DecodeString(authSecret)
	if publicErr != nil || len(publicKey) != 65 || authErr != nil || len(authKey) != 16 {
		return "", "", "", "", fmt.Errorf("invalid push subscription keys: %w", ErrConflict)
	}
	userAgent := strings.TrimSpace(p.UserAgent)
	if utf8.RuneCountInString(userAgent) > 500 {
		return "", "", "", "", fmt.Errorf("push subscription user agent is too long: %w", ErrConflict)
	}
	return endpoint, p256dh, authSecret, userAgent, nil
}

// pushEndpointHostAllowed refuses an endpoint that names this host or a
// private network outright. A hostname is checked again once resolved, when a
// delivery connects to it.
func pushEndpointHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return PushEndpointAddressAllowed(addr)
	}
	return true
}

// pushEndpointNonPublicPrefixes are the special-purpose ranges netip.Addr has
// no predicate for.
var pushEndpointNonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and broadcast
}

// PushEndpointAddressAllowed reports whether a push delivery may connect to
// addr. A browser chooses its own endpoint, so deliveries must not be steerable
// at this host or its network: loopback, private, unique-local, link-local,
// carrier-grade NAT, unspecified, multicast and reserved addresses are refused.
func PushEndpointAddressAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsMulticast() {
		return false
	}
	for _, prefix := range pushEndpointNonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (s *Store) PushSubscriptionActive(ctx context.Context, userID uuid.UUID, endpoint string) (bool, error) {
	var active bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM push_subscriptions
			WHERE user_id = $1 AND endpoint = $2 AND disabled_at IS NULL
		)
	`, userID, strings.TrimSpace(endpoint)).Scan(&active)
	return active, err
}

func (s *Store) CountActivePushSubscriptions(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM push_subscriptions
		WHERE user_id = $1 AND disabled_at IS NULL
	`, userID).Scan(&count)
	return count, err
}

func (s *Store) DisablePushSubscription(ctx context.Context, userID uuid.UUID, endpoint string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var subscriptionID uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE push_subscriptions
			SET disabled_at = COALESCE(disabled_at, now()), updated_at = now()
			WHERE user_id = $1 AND endpoint = $2
			RETURNING id
		`, userID, strings.TrimSpace(endpoint)).Scan(&subscriptionID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE push_notification_deliveries
			SET suppressed_at = now(), locked_at = NULL, last_error = 'browser subscription disabled'
			WHERE subscription_id = $1
			  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, subscriptionID)
		return err
	})
}

// disableUserPushSubscriptions disables every browser push subscription of a
// user and suppresses whatever was still queued for them.
func disableUserPushSubscriptions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE push_subscriptions
		SET disabled_at = now(), updated_at = now()
		WHERE user_id = $1 AND disabled_at IS NULL
	`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE push_notification_deliveries d
		SET suppressed_at = now(), locked_at = NULL, last_error = 'browser subscription disabled'
		FROM push_subscriptions s
		WHERE s.id = d.subscription_id AND s.user_id = $1
		  AND d.delivered_at IS NULL AND d.suppressed_at IS NULL AND d.failed_at IS NULL
	`, userID)
	return err
}

// ExpirePushNotificationEvents closes, in one statement, every queued event
// older than maxAge and reports how many it closed. The trigger queues events
// whether or not push is configured, so turning push on can find months of
// them, and expiring those a claimed batch at a time would hold new
// notifications up behind them.
func (s *Store) ExpirePushNotificationEvents(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, ErrConflict
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE push_notification_events
		SET processed_at = now(), locked_at = NULL, last_error = 'event expired before delivery'
		WHERE processed_at IS NULL AND failed_at IS NULL
		  AND created_at < now() - make_interval(secs => $1)
	`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PrunePushNotifications deletes finished events and deliveries queued more
// than retention ago, and reports how many rows it deleted. Rows still waiting
// or being retried are kept however old they are.
func (s *Store) PrunePushNotifications(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, ErrConflict
	}
	var deleted int64
	err := s.db.QueryRow(ctx, `
		WITH events AS (
			DELETE FROM push_notification_events
			WHERE (processed_at IS NOT NULL OR failed_at IS NOT NULL)
			  AND created_at < now() - make_interval(secs => $1)
			RETURNING 1
		), deliveries AS (
			DELETE FROM push_notification_deliveries
			WHERE (delivered_at IS NOT NULL OR suppressed_at IS NOT NULL OR failed_at IS NOT NULL)
			  AND created_at < now() - make_interval(secs => $1)
			RETURNING 1
		)
		SELECT (SELECT count(*) FROM events) + (SELECT count(*) FROM deliveries)
	`, retention.Seconds()).Scan(&deleted)
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) ClaimPushNotificationEvents(ctx context.Context, limit int, leaseTimeout time.Duration) ([]PushNotificationEvent, error) {
	if limit <= 0 || leaseTimeout <= 0 {
		return nil, ErrConflict
	}
	rows, err := s.db.Query(ctx, `
		WITH candidates AS (
			SELECT changelog_id
			FROM push_notification_events
			WHERE processed_at IS NULL AND failed_at IS NULL
			  AND ((locked_at IS NULL AND next_attempt_at <= now())
			       OR locked_at <= now() - make_interval(secs => $2))
			ORDER BY next_attempt_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE push_notification_events e
		SET attempt_count = e.attempt_count + 1, locked_at = now(), last_error = ''
		FROM candidates c
		WHERE e.changelog_id = c.changelog_id
		RETURNING e.changelog_id, e.attempt_count, e.created_at
	`, limit, leaseTimeout.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PushNotificationEvent, 0, limit)
	for rows.Next() {
		var event PushNotificationEvent
		if err := rows.Scan(&event.ChangelogID, &event.AttemptCount, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type pushNotificationEventContext struct {
	ProjectID   uuid.UUID
	ActorID     *uuid.UUID
	Entity      string
	Op          string
	EntityID    uuid.UUID
	IssueID     uuid.UUID
	Details     model.ProjectChangelogDetails
	CreatedAt   time.Time
	AssigneeID  *uuid.UUID
	ReporterID  *uuid.UUID
	Description string
}

func (s *Store) MaterializePushNotificationEvent(ctx context.Context, event PushNotificationEvent, maxEventAge time.Duration) (int, error) {
	if event.ChangelogID == uuid.Nil || event.AttemptCount <= 0 || maxEventAge <= 0 {
		return 0, ErrConflict
	}
	created := 0
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		eventContext, err := loadPushNotificationEventContext(ctx, tx, event)
		if err != nil {
			return err
		}
		if time.Since(eventContext.CreatedAt) > maxEventAge {
			return completePushNotificationEvent(ctx, tx, event, "event expired before delivery")
		}
		if err := loadPushNotificationIssueContext(ctx, tx, &eventContext); err != nil {
			if errors.Is(err, ErrNotFound) {
				return completePushNotificationEvent(ctx, tx, event, "issue unavailable")
			}
			return err
		}

		categoriesByUser, err := pushNotificationRecipients(ctx, tx, eventContext)
		if err != nil {
			return err
		}
		if len(categoriesByUser) == 0 {
			return completePushNotificationEvent(ctx, tx, event, "")
		}
		userIDs := make([]uuid.UUID, 0, len(categoriesByUser))
		for userID := range categoriesByUser {
			userIDs = append(userIDs, userID)
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.user_id,
			       COALESCE(p.mentions, true), COALESCE(p.assignments, true),
			       COALESCE(p.comments, false), COALESCE(p.status_changes, false),
			       COALESCE(p.due_date_changes, false)
			FROM push_subscriptions s
			JOIN users u ON u.id = s.user_id AND u.deleted_at IS NULL
			LEFT JOIN user_push_notification_preferences p ON p.user_id = s.user_id
			WHERE s.user_id = ANY($1) AND s.disabled_at IS NULL
		`, userIDs)
		if err != nil {
			return err
		}
		type subscriptionCandidate struct {
			ID          uuid.UUID
			UserID      uuid.UUID
			Preferences model.PushNotificationPreferences
		}
		var subscriptions []subscriptionCandidate
		for rows.Next() {
			var subscription subscriptionCandidate
			if err := rows.Scan(
				&subscription.ID, &subscription.UserID, &subscription.Preferences.Mentions, &subscription.Preferences.Assignments,
				&subscription.Preferences.Comments, &subscription.Preferences.StatusChanges, &subscription.Preferences.DueDateChanges,
			); err != nil {
				rows.Close()
				return err
			}
			subscriptions = append(subscriptions, subscription)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, subscription := range subscriptions {
			category, ok := preferredPushNotificationCategory(subscription.Preferences, categoriesByUser[subscription.UserID])
			if !ok {
				continue
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO push_notification_deliveries (
					changelog_id, subscription_id, user_id, project_id, issue_id, category
				)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (changelog_id, subscription_id) DO NOTHING
			`, event.ChangelogID, subscription.ID, subscription.UserID, eventContext.ProjectID, eventContext.IssueID, category)
			if err != nil {
				return err
			}
			created += int(tag.RowsAffected())
		}
		return completePushNotificationEvent(ctx, tx, event, "")
	})
	return created, err
}

func loadPushNotificationEventContext(ctx context.Context, tx pgx.Tx, event PushNotificationEvent) (pushNotificationEventContext, error) {
	var out pushNotificationEventContext
	var actorID, issueID uuid.NullUUID
	var details []byte
	err := tx.QueryRow(ctx, `
		SELECT c.project_id, c.actor_id, c.entity, c.op, c.entity_id, c.issue_id, c.details, c.created_at
		FROM push_notification_events e
		JOIN project_changelog_entries c ON c.id = e.changelog_id
		WHERE e.changelog_id = $1 AND e.attempt_count = $2
		  AND e.locked_at IS NOT NULL AND e.processed_at IS NULL AND e.failed_at IS NULL
		FOR UPDATE OF e
	`, event.ChangelogID, event.AttemptCount).Scan(
		&out.ProjectID, &actorID, &out.Entity, &out.Op, &out.EntityID, &issueID, &details, &out.CreatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return pushNotificationEventContext{}, ErrNotFound
		}
		return pushNotificationEventContext{}, err
	}
	if actorID.Valid {
		id := actorID.UUID
		out.ActorID = &id
	}
	if !issueID.Valid {
		return pushNotificationEventContext{}, ErrConflict
	}
	out.IssueID = issueID.UUID
	if err := json.Unmarshal(details, &out.Details); err != nil {
		return pushNotificationEventContext{}, err
	}
	return out, nil
}

func loadPushNotificationIssueContext(ctx context.Context, tx pgx.Tx, event *pushNotificationEventContext) error {
	var assigneeID, reporterID uuid.NullUUID
	err := tx.QueryRow(ctx, `
		SELECT assignee_id, reporter_id, description
		FROM issues
		WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
	`, event.IssueID, event.ProjectID).Scan(&assigneeID, &reporterID, &event.Description)
	if err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	if assigneeID.Valid {
		id := assigneeID.UUID
		event.AssigneeID = &id
	}
	if reporterID.Valid {
		id := reporterID.UUID
		event.ReporterID = &id
	}
	return nil
}

func completePushNotificationEvent(ctx context.Context, tx pgx.Tx, event PushNotificationEvent, note string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE push_notification_events
		SET processed_at = now(), locked_at = NULL, last_error = $3
		WHERE changelog_id = $1 AND attempt_count = $2
		  AND processed_at IS NULL AND failed_at IS NULL
	`, event.ChangelogID, event.AttemptCount, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func pushNotificationRecipients(ctx context.Context, tx pgx.Tx, event pushNotificationEventContext) (map[uuid.UUID]map[PushNotificationCategory]bool, error) {
	out := map[uuid.UUID]map[PushNotificationCategory]bool{}
	add := func(userID *uuid.UUID, category PushNotificationCategory) {
		if userID == nil || (event.ActorID != nil && *userID == *event.ActorID) {
			return
		}
		if out[*userID] == nil {
			out[*userID] = map[PushNotificationCategory]bool{}
		}
		out[*userID][category] = true
	}

	mentionUsernames := map[string]bool{}
	commentsChanged := event.Entity == "comment" && event.Op == "insert"
	statusChanged := false
	dueDateChanged := false
	if event.Details.PushNotification != nil {
		for _, username := range event.Details.PushNotification.MentionUsernames {
			mentionUsernames[username] = true
		}
		add(event.Details.PushNotification.AssigneeID, PushNotificationAssignments)
	}
	for _, change := range event.Details.Changes {
		switch change.Field {
		case "status":
			statusChanged = true
		case "due_date":
			dueDateChanged = true
		}
	}

	usernames := make([]string, 0, len(mentionUsernames))
	for username := range mentionUsernames {
		usernames = append(usernames, username)
	}
	// Anyone can write @name, so a mention notifies only people already part
	// of the work: the project's owner and members, and the issue's reporter,
	// assignee and commenters. Otherwise any account could push a notification
	// to every user on the instance from a project of its own.
	sort.Strings(usernames)
	if len(usernames) > maxPushMentionsPerChange {
		usernames = usernames[:maxPushMentionsPerChange]
	}
	if len(usernames) > 0 {
		people := []uuid.UUID{}
		for _, id := range []*uuid.UUID{event.ReporterID, event.AssigneeID} {
			if id != nil {
				people = append(people, *id)
			}
		}
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.username
			FROM users u
			WHERE u.username = ANY($1) AND u.deleted_at IS NULL
			  AND (
			      EXISTS (SELECT 1 FROM projects p WHERE p.id = $2 AND p.owner_id = u.id)
			      OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = $2 AND pm.user_id = u.id)
			      OR u.id = ANY($3)
			      OR EXISTS (SELECT 1 FROM comments c WHERE c.issue_id = $4 AND c.author_id = u.id)
			  )
		`, usernames, event.ProjectID, people, event.IssueID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var userID uuid.UUID
			var username string
			if err := rows.Scan(&userID, &username); err != nil {
				rows.Close()
				return nil, err
			}
			if mentionUsernames[username] {
				add(&userID, PushNotificationMentions)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	if commentsChanged || statusChanged || dueDateChanged {
		relevant := map[uuid.UUID]bool{}
		if event.AssigneeID != nil {
			relevant[*event.AssigneeID] = true
		}
		if event.ReporterID != nil {
			relevant[*event.ReporterID] = true
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT author_id FROM comments WHERE issue_id = $1`, event.IssueID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var userID uuid.UUID
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return nil, err
			}
			relevant[userID] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for userID := range relevant {
			if commentsChanged {
				add(&userID, PushNotificationComments)
			}
			if statusChanged {
				add(&userID, PushNotificationStatusChanges)
			}
			if dueDateChanged {
				add(&userID, PushNotificationDueDateChanges)
			}
		}
	}
	return out, nil
}

// maxPushMentionsPerChange bounds how many mentions one change can notify.
const maxPushMentionsPerChange = 50

func pushMentionUsernames(raw string) []string {
	matches := pushMentionPattern.FindAllStringSubmatch(raw, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		username := strings.ToLower(match[1])
		if seen[username] {
			continue
		}
		seen[username] = true
		out = append(out, username)
	}
	return out
}

func pushNewMentionUsernames(before, after string) []string {
	previous := stringSet(pushMentionUsernames(before))
	var out []string
	for _, username := range pushMentionUsernames(after) {
		if !previous[username] {
			out = append(out, username)
		}
	}
	return out
}

func pushNotificationChangelogData(assigneeID *uuid.UUID, mentions []string) *model.ProjectChangelogPushNotificationData {
	if assigneeID == nil && len(mentions) == 0 {
		return nil
	}
	return &model.ProjectChangelogPushNotificationData{AssigneeID: assigneeID, MentionUsernames: mentions}
}

func sameUUIDPtr(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func preferredPushNotificationCategory(preferences model.PushNotificationPreferences, categories map[PushNotificationCategory]bool) (PushNotificationCategory, bool) {
	for _, category := range []PushNotificationCategory{
		PushNotificationMentions,
		PushNotificationAssignments,
		PushNotificationComments,
		PushNotificationStatusChanges,
		PushNotificationDueDateChanges,
	} {
		if !categories[category] {
			continue
		}
		enabled := false
		switch category {
		case PushNotificationMentions:
			enabled = preferences.Mentions
		case PushNotificationAssignments:
			enabled = preferences.Assignments
		case PushNotificationComments:
			enabled = preferences.Comments
		case PushNotificationStatusChanges:
			enabled = preferences.StatusChanges
		case PushNotificationDueDateChanges:
			enabled = preferences.DueDateChanges
		}
		if enabled {
			return category, true
		}
	}
	return "", false
}

func (s *Store) RetryPushNotificationEvent(ctx context.Context, event PushNotificationEvent, nextAttemptAt time.Time, lastError string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE push_notification_events
		SET next_attempt_at = $3, locked_at = NULL, last_error = $4
		WHERE changelog_id = $1 AND attempt_count = $2
		  AND processed_at IS NULL AND failed_at IS NULL
	`, event.ChangelogID, event.AttemptCount, nextAttemptAt, lastError)
	return err
}

func (s *Store) FailPushNotificationEvent(ctx context.Context, event PushNotificationEvent, lastError string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE push_notification_events
		SET locked_at = NULL, last_error = $3, failed_at = now()
		WHERE changelog_id = $1 AND attempt_count = $2
		  AND processed_at IS NULL AND failed_at IS NULL
	`, event.ChangelogID, event.AttemptCount, lastError)
	return err
}

func (s *Store) ClaimPushNotificationDeliveries(ctx context.Context, limit int, leaseTimeout time.Duration) ([]PushNotificationDelivery, error) {
	if limit <= 0 || leaseTimeout <= 0 {
		return nil, ErrConflict
	}
	rows, err := s.db.Query(ctx, `
		WITH candidates AS (
			SELECT d.id
			FROM push_notification_deliveries d
			JOIN push_subscriptions s ON s.id = d.subscription_id
			WHERE d.delivered_at IS NULL AND d.suppressed_at IS NULL AND d.failed_at IS NULL
			  AND s.disabled_at IS NULL AND s.user_id = d.user_id
			  AND ((d.locked_at IS NULL AND d.next_attempt_at <= now())
			       OR d.locked_at <= now() - make_interval(secs => $2))
			ORDER BY d.next_attempt_at, d.created_at
			FOR UPDATE OF d SKIP LOCKED
			LIMIT $1
		)
		UPDATE push_notification_deliveries d
		SET attempt_count = d.attempt_count + 1, locked_at = now(), last_error = ''
		FROM candidates c, push_subscriptions s
		WHERE d.id = c.id AND s.id = d.subscription_id
		RETURNING d.id, d.changelog_id, d.subscription_id, d.user_id, d.project_id, d.issue_id,
		          d.category, s.endpoint, s.p256dh, s.auth_secret, d.attempt_count
	`, limit, leaseTimeout.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PushNotificationDelivery, 0, limit)
	for rows.Next() {
		var delivery PushNotificationDelivery
		if err := rows.Scan(
			&delivery.ID, &delivery.ChangelogID, &delivery.SubscriptionID, &delivery.UserID,
			&delivery.ProjectID, &delivery.IssueID, &delivery.Category, &delivery.Endpoint,
			&delivery.P256DH, &delivery.AuthSecret, &delivery.AttemptCount,
		); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) PreparePushNotificationDelivery(ctx context.Context, delivery PushNotificationDelivery) (PushNotificationPayload, bool, error) {
	enabled, err := s.pushNotificationDeliveryEnabled(ctx, delivery)
	if err != nil {
		return PushNotificationPayload{}, false, err
	}
	if !enabled {
		return PushNotificationPayload{}, false, nil
	}
	user, err := s.GetUser(ctx, delivery.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PushNotificationPayload{}, false, nil
		}
		return PushNotificationPayload{}, false, err
	}
	issue, err := s.GetIssue(ctx, delivery.IssueID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PushNotificationPayload{}, false, nil
		}
		return PushNotificationPayload{}, false, err
	}
	if issue.ProjectID != delivery.ProjectID {
		return PushNotificationPayload{}, false, nil
	}
	permissions, err := s.ProjectPermissionsForUser(ctx, user, issue.ProjectID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PushNotificationPayload{}, false, nil
		}
		return PushNotificationPayload{}, false, err
	}
	// A private issue reaches only the project's members and its reporter.
	permissions = permissions.ForIssue(user, issue)
	// A reporter who follows their issue (in a help desk, or of a private
	// issue) hears about it, but not about its due date, which only members
	// see.
	reporter := !permissions.CanRead && permissions.CanFollowIssue(user, issue)
	if !permissions.CanRead && !reporter {
		return PushNotificationPayload{}, false, nil
	}
	if reporter && delivery.Category == PushNotificationDueDateChanges {
		return PushNotificationPayload{}, false, nil
	}
	project, err := s.GetProject(ctx, issue.ProjectID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PushNotificationPayload{}, false, nil
		}
		return PushNotificationPayload{}, false, err
	}
	var actorUsername string
	var detailsRaw []byte
	var membersOnly bool
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(u.username, ''), c.details, c.members_only
		FROM project_changelog_entries c
		LEFT JOIN users u ON u.id = c.actor_id AND u.deleted_at IS NULL
		WHERE c.id = $1 AND c.project_id = $2 AND c.issue_id = $3
	`, delivery.ChangelogID, delivery.ProjectID, delivery.IssueID).Scan(&actorUsername, &detailsRaw, &membersOnly)
	if err != nil {
		if isNoRows(err) {
			return PushNotificationPayload{}, false, nil
		}
		return PushNotificationPayload{}, false, err
	}
	// A members-only comment, including one that mentions the recipient, is
	// not announced to someone who cannot read it. The flag is read at
	// delivery, so a comment made members-only after posting is covered too.
	if membersOnly && !permissions.CanReadMembersOnly {
		return PushNotificationPayload{}, false, nil
	}
	var details model.ProjectChangelogDetails
	if err := json.Unmarshal(detailsRaw, &details); err != nil {
		return PushNotificationPayload{}, false, err
	}
	body := pushNotificationBody(delivery.Category, actorUsername, details)
	// A help-desk reporter sees open, in progress or closed, never the member
	// statuses behind them: a move between two of those is news only when the
	// reporter's own status changes, and it is told in their terms.
	if reporter && delivery.Category == PushNotificationStatusChanges {
		from, to := reporterStatusChange(details)
		if from == to {
			return PushNotificationPayload{}, false, nil
		}
		body = "Status changed to " + to.Label()
	}
	if issue.Title != "" {
		body += " — " + issue.Title
	}
	payload := PushNotificationPayload{
		Title: fmt.Sprintf("%s · %s", issue.Identifier, project.Name),
		Body:  truncatePushNotificationText(body, 200),
		URL:   fmt.Sprintf("/%s/issues/%s", url.PathEscape(issue.OwnerUsername), url.PathEscape(issue.Identifier)),
		Tag:   strings.ReplaceAll(delivery.ChangelogID.String(), "-", ""),
	}
	return payload, true, nil
}

func (s *Store) pushNotificationDeliveryEnabled(ctx context.Context, delivery PushNotificationDelivery) (bool, error) {
	var enabled bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM push_subscriptions s
			LEFT JOIN user_push_notification_preferences p ON p.user_id = s.user_id
			WHERE s.id = $1 AND s.user_id = $2 AND s.disabled_at IS NULL
			  AND CASE $3
				  WHEN 'mentions' THEN COALESCE(p.mentions, true)
				  WHEN 'assignments' THEN COALESCE(p.assignments, true)
				  WHEN 'comments' THEN COALESCE(p.comments, false)
				  WHEN 'status_changes' THEN COALESCE(p.status_changes, false)
				  WHEN 'due_date_changes' THEN COALESCE(p.due_date_changes, false)
				  ELSE false
			  END
		)
	`, delivery.SubscriptionID, delivery.UserID, delivery.Category).Scan(&enabled)
	return enabled, err
}

func pushNotificationBody(category PushNotificationCategory, actorUsername string, details model.ProjectChangelogDetails) string {
	actor := "Someone"
	if actorUsername != "" {
		actor = "@" + actorUsername
	}
	switch category {
	case PushNotificationMentions:
		return actor + " mentioned you"
	case PushNotificationAssignments:
		return actor + " assigned this issue to you"
	case PushNotificationComments:
		return actor + " commented on this issue"
	case PushNotificationStatusChanges:
		if value := pushNotificationChangeValue(details, "status"); value != "" {
			return "Status changed to " + value
		}
		return "Issue status changed"
	case PushNotificationDueDateChanges:
		if value := pushNotificationChangeValue(details, "due_date"); value != "" {
			return "Due date changed to " + value
		}
		return "Issue due date changed"
	default:
		return "Issue updated"
	}
}

// reporterStatusChange reads a status change as a help-desk reporter sees it.
func reporterStatusChange(details model.ProjectChangelogDetails) (model.ReporterStatus, model.ReporterStatus) {
	for _, change := range details.Changes {
		if change.Field == "status" {
			return reporterStatusForLabel(change.From), reporterStatusForLabel(change.To)
		}
	}
	return model.ReporterStatusOpen, model.ReporterStatusOpen
}

// reporterStatusForLabel maps a status as the changelog records it, by its
// label, onto the reporter's view of it.
func reporterStatusForLabel(label string) model.ReporterStatus {
	for _, status := range []model.Status{model.StatusTodo, model.StatusInProgress, model.StatusDone, model.StatusClosed} {
		if changelogStatusLabel(status) == label {
			return model.NewReporterStatus(status)
		}
	}
	return model.ReporterStatusOpen
}

func pushNotificationChangeValue(details model.ProjectChangelogDetails, field string) string {
	for _, change := range details.Changes {
		if change.Field == field {
			return change.To
		}
	}
	return ""
}

func truncatePushNotificationText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}

func (s *Store) CompletePushNotificationDelivery(ctx context.Context, delivery PushNotificationDelivery) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE push_notification_deliveries
			SET delivered_at = now(), locked_at = NULL, last_error = ''
			WHERE id = $1 AND attempt_count = $2
			  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, delivery.ID, delivery.AttemptCount)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE push_subscriptions
			SET failure_count = 0, last_success_at = now(), updated_at = now()
			WHERE id = $1 AND disabled_at IS NULL
		`, delivery.SubscriptionID)
		return err
	})
}

func (s *Store) SuppressPushNotificationDelivery(ctx context.Context, delivery PushNotificationDelivery, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE push_notification_deliveries
		SET suppressed_at = now(), locked_at = NULL, last_error = $3
		WHERE id = $1 AND attempt_count = $2
		  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
	`, delivery.ID, delivery.AttemptCount, reason)
	return err
}

func (s *Store) RetryPushNotificationDelivery(ctx context.Context, delivery PushNotificationDelivery, nextAttemptAt time.Time, lastError string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE push_notification_deliveries
			SET next_attempt_at = $3, locked_at = NULL, last_error = $4
			WHERE id = $1 AND attempt_count = $2
			  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, delivery.ID, delivery.AttemptCount, nextAttemptAt, lastError)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE push_subscriptions
			SET failure_count = failure_count + 1, updated_at = now()
			WHERE id = $1 AND disabled_at IS NULL
		`, delivery.SubscriptionID)
		return err
	})
}

func (s *Store) FailPushNotificationDelivery(ctx context.Context, delivery PushNotificationDelivery, lastError string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE push_notification_deliveries
		SET failed_at = now(), locked_at = NULL, last_error = $3
		WHERE id = $1 AND attempt_count = $2
		  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
	`, delivery.ID, delivery.AttemptCount, lastError)
	return err
}

func (s *Store) DisableRejectedPushSubscription(ctx context.Context, delivery PushNotificationDelivery, lastError string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE push_subscriptions
			SET disabled_at = COALESCE(disabled_at, now()),
			    failure_count = failure_count + 1,
			    updated_at = now()
			WHERE id = $1
		`, delivery.SubscriptionID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE push_notification_deliveries
			SET suppressed_at = now(), locked_at = NULL, last_error = $2
			WHERE subscription_id = $1
			  AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, delivery.SubscriptionID, lastError)
		return err
	})
}
