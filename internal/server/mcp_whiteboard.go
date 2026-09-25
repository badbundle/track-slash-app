package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type mcpWhiteboardPageInput struct {
	mcpProjectInput
	Page string `json:"page" jsonschema:"whiteboard page ref, for example whiteboard-1"`
}

type mcpCreateWhiteboardPageInput struct {
	mcpProjectInput
	Title string `json:"title" jsonschema:"page title, 1 to 200 characters"`
	Body  string `json:"body,omitempty" jsonschema:"optional Markdown body, at most 100000 characters"`
}

type mcpUpdateWhiteboardPageInput struct {
	mcpWhiteboardPageInput
	Title *string `json:"title,omitempty" jsonschema:"new page title"`
	Body  *string `json:"body,omitempty" jsonschema:"new Markdown body; an empty string clears it"`
}

// mcpWhiteboardPageIn resolves a page ref inside a project the caller has
// already been authorized for, so a page's existence is never revealed to an
// outsider.
func (s *Server) mcpWhiteboardPageIn(ctx context.Context, project model.Project, ref string) (model.WhiteboardPage, error) {
	number, err := mcpTypedRef(ref, "whiteboard")
	if err != nil {
		return model.WhiteboardPage{}, err
	}
	return s.store.GetWhiteboardPageByProjectNumber(ctx, project.ID, number)
}

// mcpWhiteboardPage resolves a page for a reader of its project.
func (s *Server) mcpWhiteboardPage(ctx context.Context, auth authContext, input mcpWhiteboardPageInput) (model.WhiteboardPage, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.WhiteboardPage{}, err
	}
	return s.mcpWhiteboardPageIn(ctx, project, input.Page)
}

// mcpWritableWhiteboardPage resolves a page for a writer of its project.
func (s *Server) mcpWritableWhiteboardPage(ctx context.Context, auth authContext, input mcpWhiteboardPageInput) (model.WhiteboardPage, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.WhiteboardPage{}, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return model.WhiteboardPage{}, err
	}
	return s.mcpWhiteboardPageIn(ctx, project, input.Page)
}

func (s *Server) mcpListWhiteboardPages(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.WhiteboardPagesCursor
	if input.Cursor != "" {
		var c store.WhiteboardPagesCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	pages, hasMore, err := s.store.ListWhiteboardPages(ctx, store.ListWhiteboardPagesParams{ProjectID: project.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
	}
	return mcpPageOut(pages, whiteboardPagesNextCursor(pages, hasMore)), nil
}

func (s *Server) mcpGetWhiteboardPage(ctx context.Context, req *mcp.CallToolRequest, input mcpWhiteboardPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	page, err := s.mcpWhiteboardPage(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"page": page}, nil
}

func (s *Server) mcpCreateWhiteboardPage(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateWhiteboardPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	title, err := validateProjectContextTitle(input.Title)
	if err != nil {
		return nil, validationError(err.Error())
	}
	body, err := validateProjectContextBody(input.Body)
	if err != nil {
		return nil, validationError(err.Error())
	}
	page, err := s.store.CreateWhiteboardPage(ctx, store.CreateWhiteboardPageParams{
		ProjectID:   project.ID,
		Title:       title,
		Body:        body,
		CreatedByID: auth.User.ID,
	})
	if err != nil {
		return nil, err // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
	}
	return mcpToolOutput{"page": page}, nil
}

func (s *Server) mcpUpdateWhiteboardPage(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateWhiteboardPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	page, err := s.mcpWritableWhiteboardPage(ctx, auth, input.mcpWhiteboardPageInput)
	if err != nil {
		return nil, err
	}
	params := store.UpdateWhiteboardPageParams{ID: page.ID, UpdatedByID: auth.User.ID}
	if input.Title != nil {
		title, err := validateProjectContextTitle(*input.Title)
		if err != nil {
			return nil, validationError(err.Error())
		}
		params.Title = &title
	}
	if input.Body != nil {
		body, err := validateProjectContextBody(*input.Body)
		if err != nil {
			return nil, validationError(err.Error())
		}
		params.Body = &body
	}
	updated, err := s.store.UpdateWhiteboardPage(ctx, params)
	if err != nil {
		return nil, err // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
	}
	return mcpToolOutput{"page": updated}, nil
}

func (s *Server) mcpDeleteWhiteboardPage(ctx context.Context, req *mcp.CallToolRequest, input mcpWhiteboardPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	page, err := s.mcpWritableWhiteboardPage(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteWhiteboardPage(ctx, page.ID); err != nil {
		return nil, err // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
	}
	return mcpOK(), nil
}
