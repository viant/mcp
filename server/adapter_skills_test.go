package server

import (
	"context"
	"github.com/stretchr/testify/require"
	format "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
	"testing"
	"testing/fstest"
)

func TestAdapterNativeSkillsUsesStandardCapturedResources(t *testing.T) {
	ctx := context.Background()
	body := "---\nname: review\ndescription: Original\n---\nCaptured bytes"
	files := fstest.MapFS{"SKILL.md": {Data: []byte(body)}, "note.txt": {Data: []byte("note")}}
	compiled, err := (format.Compiler{Source: files}).Compile(ctx, "skill://workspace/review/SKILL.md")
	require.NoError(t, err)
	files["SKILL.md"].Data = []byte("changed after capture")
	srv, err := New(WithNewHandler(protocol.WithDefaultHandler(ctx, func(h *protocol.DefaultHandler) error { return h.RegisterStaticSkill(compiled) })))
	require.NoError(t, err)
	handler := srv.NewHandler(ctx, nil).(*Handler)
	adapter := NewAdapter(handler)
	initialized, err := adapter.Initialize(ctx)
	require.NoError(t, err)
	require.Contains(t, initialized.Capabilities.Extensions, schema.SkillsExtension)
	listed, err := adapter.ListSkills(ctx, nil)
	require.NoError(t, err)
	require.Len(t, listed.Skills, 1)
	got, err := adapter.GetSkill(ctx, listed.Skills[0].Uri, client.WithJsonRpcRequestId(50))
	require.NoError(t, err)
	require.Equal(t, listed.Skills[0], got.Skill)
	read, err := adapter.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: got.Skill.Uri})
	require.NoError(t, err)
	require.Equal(t, body, read.Contents[0].Text)
	_, err = adapter.GetSkill(ctx, "skill://workspace/missing/SKILL.md")
	require.Error(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = adapter.ListSkills(canceled, nil)
	require.Error(t, err)
}
