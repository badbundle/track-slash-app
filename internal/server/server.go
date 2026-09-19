package server

import (
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/passkeys"
	"github.com/bradleymackey/track-slash/internal/realtime"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

// corsAllowedMethods must cover every method the router answers. go-chi/cors
// aborts a preflight whose requested method is absent, which silently makes the
// route unreachable from any cross-origin browser client. HEAD is included even
// though no route registers it, because serveHEADAsGET answers it everywhere.
var corsAllowedMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

type Server struct {
	store                *store.Store
	hub                  *realtime.Hub
	corsAllowedOrigins   []string
	apiWebSocketOrigins  realtime.OriginPolicy
	uiWebSocketOrigins   realtime.OriginPolicy
	devReload            bool
	objectStorage        *objectstorage.Service
	passkeys             *passkeys.Service
	publicOrigin         string
	csrfRandom           io.Reader
	secureCookies        bool
	httpsDeployment      bool
	sessionTTL           time.Duration
	authLimiter          *authRateLimiter
	trustedProxyCIDRs    []net.IPNet
	requestTimeout       time.Duration
	authRequestTimeout   time.Duration
	uploadTimeout        time.Duration
	previewTermsRequired bool
	webPushPublicKey     string
	githubIntegration    *githubintegration.Service
}

type Options struct {
	CORSAllowedOrigins   []string
	PublicOrigin         string
	SessionTTL           time.Duration
	AuthRateLimit        AuthRateLimitOptions
	TrustedProxyCIDRs    []net.IPNet
	RequestTimeout       time.Duration
	AuthRequestTimeout   time.Duration
	UploadTimeout        time.Duration
	PreviewTermsRequired bool
	DevReload            bool
	ObjectStorage        *objectstorage.Service
	WebPushPublicKey     string
	GitHubIntegration    *githubintegration.Service
}

// New constructs a Server. corsAllowedOrigins is a list of exact browser
// origins allowed to use the cross-origin API. WebSocket browser origins are
// also constrained by PublicOrigin when using NewWithOptions.
func New(s *store.Store, hub *realtime.Hub, corsAllowedOrigins []string) *Server {
	return NewWithOptions(s, hub, Options{CORSAllowedOrigins: corsAllowedOrigins})
}

func NewWithOptions(s *store.Store, hub *realtime.Hub, opts Options) *Server {
	var passkeyService *passkeys.Service
	if s != nil {
		passkeyService = passkeys.New(s, opts.PublicOrigin)
	}
	publicOrigin, _ := url.Parse(opts.PublicOrigin)
	apiWebSocketOrigins, uiWebSocketOrigins := webSocketOriginPolicies(opts.PublicOrigin, opts.CORSAllowedOrigins)
	sessionTTL := opts.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 7 * 24 * time.Hour
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 15 * time.Second
	}
	authRequestTimeout := opts.AuthRequestTimeout
	if authRequestTimeout <= 0 {
		authRequestTimeout = 30 * time.Second
	}
	uploadTimeout := opts.UploadTimeout
	if uploadTimeout <= 0 {
		uploadTimeout = 2 * time.Minute
	}
	return &Server{
		store:                s,
		hub:                  hub,
		corsAllowedOrigins:   opts.CORSAllowedOrigins,
		apiWebSocketOrigins:  apiWebSocketOrigins,
		uiWebSocketOrigins:   uiWebSocketOrigins,
		devReload:            opts.DevReload,
		objectStorage:        opts.ObjectStorage,
		passkeys:             passkeyService,
		publicOrigin:         opts.PublicOrigin,
		csrfRandom:           rand.Reader,
		secureCookies:        publicOrigin != nil && publicOrigin.Scheme == "https",
		httpsDeployment:      publicOrigin != nil && publicOrigin.Scheme == "https",
		sessionTTL:           sessionTTL,
		authLimiter:          newAuthRateLimiter(opts.AuthRateLimit),
		trustedProxyCIDRs:    opts.TrustedProxyCIDRs,
		requestTimeout:       requestTimeout,
		authRequestTimeout:   authRequestTimeout,
		uploadTimeout:        uploadTimeout,
		previewTermsRequired: opts.PreviewTermsRequired,
		webPushPublicKey:     opts.WebPushPublicKey,
		githubIntegration:    opts.GitHubIntegration,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(exposeRequestID)
	r.Use(s.securityHeaders)
	r.Use(s.requestDeadline)
	if s.devReload {
		r.Use(s.devReloadMiddleware)
	}
	if len(s.corsAllowedOrigins) > 0 {
		// The OAuth discovery and token endpoints answer any origin and set
		// their own CORS headers, so they are routed around this middleware.
		// go-chi/cors answers a preflight from an origin outside the allow list
		// with a bare 200 and no headers, without calling the handler beneath,
		// which a browser reads as a refusal.
		r.Use(exceptOAuthPublicPaths(cors.Handler(cors.Options{
			AllowedOrigins:   s.corsAllowedOrigins,
			AllowedMethods:   corsAllowedMethods,
			AllowedHeaders:   []string{"Authorization", "Content-Type", "Accept", "If-Match", "Last-Event-ID", "MCP-Protocol-Version", "Mcp-Session-Id"},
			ExposedHeaders:   []string{"X-Request-ID", "Mcp-Session-Id"},
			AllowCredentials: false,
			MaxAge:           300,
		})))
	}

	r.Use(serveHEADAsGET)
	r.Use(redirectUITrailingSlash)

	// chi pushes these into any sub-router that has none of its own, so the
	// /api/v1 sub-router sets JSON responders explicitly to avoid answering
	// JSON clients with HTML.
	r.NotFound(s.uiOptionalAuth(s.uiNotFound))
	r.MethodNotAllowed(s.uiOptionalAuth(s.uiMethodNotAllowed))

	if s.devReload {
		r.Get(devReloadPath, s.devReloadEvents)
	}
	s.mountMCPRoutes(r)
	s.mountUIRoutes(r)

	r.Route("/api/v1", func(r chi.Router) {
		r.NotFound(apiNotFound)
		r.MethodNotAllowed(apiMethodNotAllowed)

		r.Get("/healthz", s.healthz)
		r.Post("/accounts", s.authIPRateLimited(s.createAccount))
		r.Post("/accounts/passkey/options", s.authIPRateLimited(s.createPasskeyAccountOptions))
		r.Post("/accounts/passkey", s.authIPRateLimited(s.createPasskeyAccount))
		r.Post("/session", s.authIPRateLimited(s.createSession))
		r.Post("/session/passkey/options", s.authIPRateLimited(s.createPasskeySessionOptions))
		r.Post("/session/passkey", s.authIPRateLimited(s.createPasskeySession))

		// The request-deadline middleware exempts this long-lived connection.
		if s.hub != nil {
			r.Method(http.MethodGet, "/ws", s.authMiddleware(s.hub.Handler(s.apiWebSocketOrigins, s.authorizeTopic)))
		}

		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.getMe)
			r.Post("/me/profile-image", s.createMyProfileImage)
			r.Delete("/me/profile-image", s.deleteMyProfileImage)
			r.Patch("/me/settings", s.updateMySettings)
			r.Get("/me/password-login", s.getMyPasswordLogin)
			r.Patch("/me/password-login", s.updateMyPasswordLogin)
			r.Get("/me/passkeys", s.listMyPasskeys)
			r.Post("/me/reauth/password", s.authIPRateLimited(s.authAccountRateLimited(s.createPasswordReauth)))
			r.Post("/me/reauth/passkey/options", s.authIPRateLimited(s.authAccountRateLimited(s.createPasskeyReauthOptions)))
			r.Post("/me/reauth/passkey", s.authIPRateLimited(s.authAccountRateLimited(s.createPasskeyReauth)))
			r.Post("/me/passkeys/options", s.createMyPasskeyOptions)
			r.Post("/me/passkeys", s.createMyPasskey)
			r.Delete("/me/passkeys/{id}", s.revokeMyPasskey)
			r.Get("/me/tokens", s.listMyTokens)
			r.Post("/me/tokens", s.createMyToken)
			r.Delete("/me/tokens/{id}", s.revokeMyToken)
			r.Delete("/tokens/{id}", s.revokeToken)

			r.Route("/users", func(r chi.Router) {
				r.Post("/", s.createUser)
				r.Get("/", s.listUsers)
				r.Get("/{id}/profile-image/content", s.getUserProfileImageContent)
				r.Get("/{id}/profile-image/thumbnail/content", s.getUserProfileImageThumbnailContent)
				r.Get("/{id}", s.getUser)
				r.Delete("/{id}", s.deleteUser)
				r.Post("/{id}/tokens", s.createUserToken)
				r.Get("/{id}/tokens", s.listUserTokens)
			})

			r.Route("/projects", func(r chi.Router) {
				r.Post("/", s.createProject)
				r.Get("/", s.listProjects)
			})

			r.Route("/{owner}", func(r chi.Router) {
				r.Route("/projects/{key}", func(r chi.Router) {
					r.Get("/", s.getProject)
					r.Get("/access", s.getProjectAccess)
					r.Patch("/access", s.updateProjectAccess)
					r.Patch("/", s.updateProject)
					r.Delete("/", s.deleteProject)
					r.Post("/image", s.createProjectImage)
					r.Delete("/image", s.deleteProjectImage)
					r.Get("/image/content", s.getProjectImageContent)
					r.Get("/image/thumbnail/content", s.getProjectImageThumbnailContent)
					r.Put("/favorite", s.favoriteProject)
					r.Delete("/favorite", s.unfavoriteProject)
					r.Get("/changelog", s.listProjectChangelog)
					r.Get("/stats", s.getProjectStats)
					r.Get("/github/connections", s.listGitHubConnections)
					r.Post("/github/connections", s.connectGitHubRepository)
					r.Delete("/github/connections/{connectionID}", s.disconnectGitHubRepository)
					r.Get("/members/search", s.searchProjectMembers)
					r.Get("/members/candidates", s.searchAvailableProjectMembers)
					r.Get("/members", s.listProjectMembers)
					r.Get("/assignees", s.listProjectAssignees)
					r.Put("/members/{username}", s.grantProjectMember)
					r.Delete("/members/{username}", s.revokeProjectMember)
					r.Get("/blocks", s.listProjectBlocks)
					r.Put("/blocks/{username}", s.blockProjectUser)
					r.Delete("/blocks/{username}", s.unblockProjectUser)
					r.Post("/objects", s.createStorageObject)
					r.Get("/objects", s.listStorageObjects)
					r.Get("/objects/{objectRef}", s.getStorageObject)
					r.Get("/objects/{objectRef}/content", s.getStorageObjectContent)
					r.Delete("/objects/{objectRef}", s.deleteStorageObject)
					r.Post("/attachments", s.createProjectAttachment)
					r.Get("/attachments", s.listProjectAttachments)
					r.Get("/attachments/{objectRef}/content", s.getProjectAttachmentContent)
					r.Delete("/attachments/{objectRef}", s.deleteProjectAttachment)
					r.Post("/context-links", s.createIssueContextLinks)
					r.Post("/context", s.createProjectContext)
					r.Get("/context", s.listProjectContexts)
					r.Post("/context/{contextRef}/attachments", s.createContextAttachment)
					r.Get("/context/{contextRef}/attachments", s.listContextAttachments)
					r.Get("/context/{contextRef}/attachments/{objectRef}/content", s.getContextAttachmentContent)
					r.Delete("/context/{contextRef}/attachments/{objectRef}", s.deleteContextAttachment)
					r.Get("/context/{contextRef}", s.getProjectContext)
					r.Patch("/context/{contextRef}", s.updateProjectContext)
					r.Delete("/context/{contextRef}", s.deleteProjectContext)
					r.Post("/tags", s.createIssueTag)
					r.Get("/tags", s.listIssueTags)
					r.Get("/tags/{tagRef}", s.getIssueTag)
					r.Patch("/tags/{tagRef}", s.updateIssueTag)
					r.Delete("/tags/{tagRef}", s.deleteIssueTag)
					r.Post("/issues", s.createIssue)
					r.Get("/issues", s.listIssues)
					r.Get("/issues/deleted", s.listDeletedIssues)
					r.Patch("/sprints/planned-order", s.reorderPlannedSprints)
					r.Post("/sprints", s.createSprint)
					r.Get("/sprints", s.listProjectSprints)
					r.Get("/sprints/{sprintRef}", s.getSprint)
					r.Get("/sprints/{sprintRef}/history/issues", s.listSprintHistoryIssues)
					r.Patch("/sprints/{sprintRef}", s.updateSprint)
					r.Delete("/sprints/{sprintRef}", s.deleteSprint)
					r.Post("/sprints/{sprintRef}/complete", s.completeSprint)
					r.Post("/sprints/{sprintRef}/attachments", s.createSprintAttachment)
					r.Get("/sprints/{sprintRef}/attachments", s.listSprintAttachments)
					r.Get("/sprints/{sprintRef}/attachments/{objectRef}/content", s.getSprintAttachmentContent)
					r.Delete("/sprints/{sprintRef}/attachments/{objectRef}", s.deleteSprintAttachment)
					r.Get("/links/{linkRef}", s.getIssueLink)
					r.Patch("/links/{linkRef}", s.updateIssueLink)
					r.Delete("/links/{linkRef}", s.deleteIssueLink)
				})
				r.Route("/issues", func(r chi.Router) {
					r.Get("/", s.batchIssues)
					r.Get("/{issueRef}", s.getIssue)
					r.Patch("/{issueRef}", s.updateIssue)
					r.Delete("/{issueRef}", s.deleteIssue)
					r.Post("/{issueRef}/restore", s.restoreIssue)
					r.Post("/{issueRef}/sub-issues", s.createSubIssue)
					r.Get("/{issueRef}/sub-issues", s.listSubIssues)
					r.Post("/{issueRef}/comments", s.createComment)
					r.Get("/{issueRef}/comments", s.listComments)
					r.Get("/{issueRef}/comments/{commentRef}", s.getComment)
					r.Patch("/{issueRef}/comments/{commentRef}", s.updateComment)
					r.Delete("/{issueRef}/comments/{commentRef}", s.deleteComment)
					r.Post("/{issueRef}/attachments", s.createIssueAttachment)
					r.Get("/{issueRef}/attachments", s.listIssueAttachments)
					r.Get("/{issueRef}/attachments/{objectRef}/content", s.getIssueAttachmentContent)
					r.Delete("/{issueRef}/attachments/{objectRef}", s.deleteIssueAttachment)
					r.Post("/{issueRef}/context", s.createIssueContext)
					r.Get("/{issueRef}/context", s.listIssueContexts)
					r.Delete("/{issueRef}/context/{contextRef}", s.deleteIssueContextLink)
					r.Post("/{issueRef}/tags", s.attachIssueTag)
					r.Get("/{issueRef}/tags", s.listTagsForIssue)
					r.Delete("/{issueRef}/tags/{tagRef}", s.detachIssueTag)
					r.Post("/{issueRef}/links", s.createIssueLink)
					r.Get("/{issueRef}/links", s.listIssueLinks)
					r.Get("/{issueRef}/github-links", s.listGitHubIssueLinks)
					r.Post("/{issueRef}/github-links", s.createGitHubIssueLink)
					r.Delete("/{issueRef}/github-links/{githubLinkID}", s.deleteGitHubIssueLink)
					r.Post("/{issueRef}/github-links/{githubLinkID}/refresh", s.refreshGitHubIssueLink)
				})
			})
		})
	})

	return r
}

func webSocketOriginPolicies(publicOrigin string, corsAllowedOrigins []string) (realtime.OriginPolicy, realtime.OriginPolicy) {
	apiPolicy := realtime.OriginPolicy{AllowMissingOrigin: true}
	uiPolicy := realtime.OriginPolicy{}
	if publicOrigin == "" {
		apiPolicy.AllowLocalhostOrigins = true
		uiPolicy.AllowLocalhostOrigins = true
	} else {
		apiPolicy.AllowedOrigins = append(apiPolicy.AllowedOrigins, publicOrigin)
		uiPolicy.AllowedOrigins = append(uiPolicy.AllowedOrigins, publicOrigin)
	}
	for _, origin := range corsAllowedOrigins {
		if !containsOrigin(apiPolicy.AllowedOrigins, origin) {
			apiPolicy.AllowedOrigins = append(apiPolicy.AllowedOrigins, origin)
		}
	}
	return apiPolicy, uiPolicy
}

func containsOrigin(origins []string, candidate string) bool {
	for _, origin := range origins {
		if origin == candidate {
			return true
		}
	}
	return false
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// exposeRequestID copies chi's RequestID context value into the response so
// clients can quote it back in bug reports. Cheap and aids debugging.
func exposeRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-ID", id)
		}
		next.ServeHTTP(w, r)
	})
}
