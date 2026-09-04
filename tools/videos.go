package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Video library tools.
//
// The point of these is that an agent can publish a video end to end: create a
// library if there is not one, put a file in it, wait for it to be playable and
// hand back an embed snippet. Everything else here exists to answer the two
// questions that follow — how is it doing, and is it actually free to watch.
//
// Uploading is the one operation whose shape differs from every other tool in
// this server: the bytes go straight to object storage rather than through the
// API, so the file must exist on the machine running this server. There is no
// inline-content form, because a base64 video in a tool call would be a
// gigabyte of JSON through a model's context.

// ---- upload-video ----

type uploadVideoArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Library   string `json:"library"   jsonschema:"Video library name. An existing video service with this name is reused; otherwise one is created."`
	Path      string `json:"path"      jsonschema:"Absolute path to the video file on the machine running this MCP server. Bytes are uploaded directly to storage, so the file is never read into the conversation."`
	Title     string `json:"title,omitempty" jsonschema:"Title for the video. Defaults to the file name."`
	Wait      *bool  `json:"wait,omitempty"  jsonschema:"Wait for encoding to finish so the returned embed snippet works immediately. Default true. Encoding a long file can take several minutes."`
	MaxHeight int    `json:"max_height,omitempty" jsonschema:"Ladder ceiling for a newly created library: 720 (default) or 1080. 1080p roughly doubles storage per video for a rung few mobile viewers select."`
}

func handleUploadVideo(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args uploadVideoArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if strings.TrimSpace(args.Library) == "" {
		return text("library is required"), nil, nil
	}
	if strings.TrimSpace(args.Path) == "" {
		return text("path is required — the file must exist on the machine running this server"), nil, nil
	}

	svcs := services(api, args.Workspace, args.Project, args.Env)
	librarySlug, created, result := resolveVideoLibrary(ctx, svcs, args.Library, args.MaxHeight)
	if result != nil {
		return result, nil, nil
	}

	client := svcs.Video(librarySlug)
	// No progress callback: there is nowhere to render one in a tool call, and
	// the bytes are going out over the server's own connection regardless.
	video, err := client.Upload(ctx, args.Path, args.Title, nil)
	if err != nil {
		return apiErr("upload video", err), nil, nil
	}

	wait := true
	if args.Wait != nil {
		wait = *args.Wait
	}
	if wait {
		ready, err := client.WaitUntilReady(ctx, video.Slug, 5*time.Second)
		if err != nil {
			// The upload succeeded; only the encode did not. Reporting the
			// video id alongside the failure is what lets a caller act on it
			// rather than upload the same file again.
			return jsonText(map[string]any{
				"service": librarySlug,
				"video":   video.Slug,
				"status":  "failed",
				"error":   err.Error(),
			}), nil, nil
		}
		video = ready
	}

	return jsonText(map[string]any{
		"service":         librarySlug,
		"library_created": created,
		"video":           video.Slug,
		"title":           video.Title,
		"status":          video.Status,
		"duration_ms":     video.DurationMS,
		"embed_url":       video.EmbedURL,
		"playback_url":    video.PlaybackURL,
		"iframe_snippet":  video.IframeSnippet,
		"script_snippet":  video.ScriptSnippet,
		"note": "Playback leaves through the platform's zero-rated address, so watching " +
			"this costs the viewer no data on supported Nigerian networks.",
	}), nil, nil
}

// resolveVideoLibrary finds a video service by name, creating one if there is
// none. Reusing by name is what makes repeated calls put videos in one library
// rather than creating a new service each time.
func resolveVideoLibrary(
	ctx context.Context,
	svcs *cloud.ServicesClient,
	name string,
	maxHeight int,
) (slug string, created bool, failure *mcp.CallToolResult) {
	existing, err := svcs.List(ctx)
	if err != nil {
		return "", false, apiErr("list services", err)
	}
	for _, s := range existing {
		if s.Type == cloud.ServiceTypeVideo && strings.EqualFold(s.Name, name) {
			return s.Slug, false, nil
		}
	}

	svc, err := svcs.Create(ctx, cloud.CreateServiceInput{
		Name: name,
		Type: cloud.ServiceTypeVideo,
		Video: &cloud.VideoInput{
			Name:      name,
			MaxHeight: maxHeight,
		},
	})
	if err != nil {
		return "", false, apiErr("create video library", err)
	}
	return svc.Slug, true, nil
}

// ---- list-videos ----

type listVideosArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Video library service slug"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum videos to return, default 50"`
}

func handleListVideos(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args listVideosArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	list, err := services(api, args.Workspace, args.Project, args.Env).
		Video(args.Service).List(ctx, args.Limit, 0)
	if err != nil {
		return apiErr("list videos", err), nil, nil
	}
	return jsonText(list), nil, nil
}

// ---- get-video ----

type getVideoArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Video library service slug"`
	Video     string `json:"video"     jsonschema:"Video id"`
}

func handleGetVideo(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args getVideoArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	video, err := services(api, args.Workspace, args.Project, args.Env).
		Video(args.Service).GetVideo(ctx, args.Video)
	if err != nil {
		return apiErr("get video", err), nil, nil
	}
	return jsonText(video), nil, nil
}

// ---- get-video-stats ----

type videoStatsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Video library service slug"`
	Video     string `json:"video,omitempty" jsonschema:"Video id. Omit for figures across the whole library, which also returns the most watched videos."`
	From      string `json:"from,omitempty"  jsonschema:"Start date, YYYY-MM-DD. Defaults to four weeks ago."`
	To        string `json:"to,omitempty"    jsonschema:"End date, YYYY-MM-DD. Defaults to today."`
}

func handleGetVideoStats(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args videoStatsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	client := services(api, args.Workspace, args.Project, args.Env).Video(args.Service)

	var (
		stats *cloud.VideoAnalytics
		err   error
	)
	if strings.TrimSpace(args.Video) == "" {
		stats, err = client.LibraryAnalytics(ctx, args.From, args.To)
	} else {
		stats, err = client.Analytics(ctx, args.Video, args.From, args.To)
	}
	if err != nil {
		return apiErr("get video statistics", err), nil, nil
	}
	return jsonText(stats), nil, nil
}

// ---- add-video-captions ----

type addVideoCaptionsArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Video library service slug"`
	Video     string `json:"video"     jsonschema:"Video id"`
	Language  string `json:"language"  jsonschema:"Language code, e.g. en or ha"`
	Label     string `json:"label,omitempty" jsonschema:"Label shown in the player's caption menu. Defaults to the language code."`
	Content   string `json:"content"   jsonschema:"The WebVTT file as text. Must begin with the line WEBVTT."`
}

func handleAddVideoCaptions(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args addVideoCaptionsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(args.Content), "WEBVTT") {
		return text("captions must be a WebVTT file, which begins with the line \"WEBVTT\""), nil, nil
	}
	track, err := services(api, args.Workspace, args.Project, args.Env).
		Video(args.Service).
		AddCaptions(ctx, args.Video, args.Language, args.Label, []byte(args.Content))
	if err != nil {
		return apiErr("add video captions", err), nil, nil
	}
	return jsonText(track), nil, nil
}

// ---- set-video-playback-domain ----

type setVideoDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Video library service slug"`
	Domain    string `json:"domain"    jsonschema:"Playback hostname, e.g. video.example.com. Pass an empty string to detach the current one."`
}

func handleSetVideoDomain(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args setVideoDomainArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	svcs := services(api, args.Workspace, args.Project, args.Env)
	lib, err := svcs.Video(args.Service).SetPlaybackDomain(ctx, args.Domain)
	if err != nil {
		return apiErr("set video playback domain", err), nil, nil
	}
	// Routing for a custom domain is a cluster resource, unlike the platform
	// hostname which serves immediately, so it needs a deploy to take effect.
	if strings.TrimSpace(args.Domain) != "" {
		if _, err := svcs.Deploy(ctx, args.Service); err != nil {
			return apiErr("deploy video library", err), nil, nil
		}
	}

	out := map[string]any{
		"playback_domain":     lib.PlaybackDomain,
		"domain_cname_target": lib.DomainCNAMETarget,
		"playback_url":        lib.PlaybackURL,
	}
	if lib.PlaybackDomain != "" {
		out["next_step"] = fmt.Sprintf(
			"point a CNAME for %s at %s; playback continues on the platform hostname until DNS propagates, "+
				"which is on the same zero-rated address, so nothing is metered in the meantime",
			lib.PlaybackDomain, lib.DomainCNAMETarget)
	}
	return jsonText(out), nil, nil
}

// RegisterVideoTools adds the video library tools to the server.
func RegisterVideoTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "upload-video",
		Description: "Upload a video to Simplifyd and get an embeddable player back. Creates the video library " +
			"if one with this name does not exist. The file is read from a path on the machine running this " +
			"server and uploaded straight to storage, so its size is not bounded by the conversation. " +
			"Waits for encoding by default, so the returned embed snippet works immediately. " +
			"Playback is served from the platform's zero-rated address, which means watching costs the viewer " +
			"no data on supported Nigerian networks — the reason to host video here rather than on YouTube.",
	}, handleUploadVideo)

	addTool(s, &mcp.Tool{
		Name: "list-videos",
		Description: "List the videos in a video library, with their encoding status, length and stored size. " +
			"A video that is still processing reports its percentage.",
	}, handleListVideos)

	addTool(s, &mcp.Tool{
		Name: "get-video",
		Description: "Get one video's status, encoded renditions, caption tracks and embed snippets. " +
			"On failure the encoder's own message is returned, which usually says exactly what about the " +
			"source file could not be handled.",
	}, handleGetVideo)

	addTool(s, &mcp.Tool{
		Name: "get-video-stats",
		Description: "Get playback statistics for a video, or for a whole library when video is omitted: views, " +
			"unique viewers, watch time, completion rate, the retention curve, and which rungs of the quality " +
			"ladder the audience actually played. Also breaks watch time down by mobile carrier and reports " +
			"how much of it was zero-rated — the share that was genuinely free to the people who watched it.",
	}, handleGetVideoStats)

	addTool(s, &mcp.Tool{
		Name: "add-video-captions",
		Description: "Attach a WebVTT caption track to a video. The file is passed inline as text, so captions " +
			"generated in the conversation can be added without touching the filesystem.",
	}, handleAddVideoCaptions)

	addTool(s, &mcp.Tool{
		Name: "set-video-playback-domain",
		Description: "Serve a video library's playback on a custom hostname, or detach the current one by passing " +
			"an empty domain. Returns the CNAME target the domain must point at.",
	}, handleSetVideoDomain)
}
