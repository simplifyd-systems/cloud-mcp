package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Computer use: an AI agent given a desktop machine to work on.
//
// The tools follow the shape of the computer-use tools models are trained on —
// look with a screenshot, act with the mouse and keyboard, look again — so an
// agent already knows how to use them. Two departures, both to save round
// trips: every action returns a fresh screenshot unless asked not to, and
// there is a shell (computer-bash) for the work a terminal does better than a
// mouse.

// screenshotSettle is how long an action is given to take effect before the
// screenshot that follows it. Long enough for a menu to open or a button to
// repaint; a page load needs computer-action with action "wait".
const screenshotSettle = 400 * time.Millisecond

type computerTarget struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Slug or name of the desktop machine"`
}

// computerFor resolves the machine, accepting its name as well as its slug:
// an agent that created a computer called "research" should be able to say so.
func computerFor(ctx context.Context, api *cloud.Client, t computerTarget) (*cloud.ComputerClient, error) {
	svcs := services(api, t.Workspace, t.Project, t.Env)
	slug := t.Service
	if !isSlug(slug) {
		list, err := svcs.List(ctx)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, s := range list {
			if s.VM.HasDesktop() {
				if s.Name == slug {
					return svcs.Computer(s.Slug), nil
				}
				names = append(names, s.Name)
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("no desktop machine named %q, and this environment has none; create one with create-computer", slug)
		}
		return nil, notFound("desktop machine", slug, names)
	}
	return svcs.Computer(slug), nil
}

// computerErr is apiErr for the computer tools, with one difference: a 400 or
// 503 keeps its message. Those are the answers an agent acts on — a click
// outside the screen, a key name xdotool does not know, a machine still
// booting — and both are written by the platform's own agent and API, not
// passed up from a backend, so there is nothing in them to leak.
func computerErr(op string, err error) *mcp.CallToolResult {
	var apiError *cloud.APIError
	if errors.As(err, &apiError) && apiError.Message != "" &&
		(apiError.StatusCode == http.StatusBadRequest || apiError.StatusCode == http.StatusServiceUnavailable) {
		return errResult("%s: %s", op, apiError.Message)
	}
	return apiErr(op, err)
}

func screenshotResult(ctx context.Context, computer *cloud.ComputerClient, note string) *mcp.CallToolResult {
	png, err := computer.Screenshot(ctx)
	if err != nil {
		return computerErr("take screenshot", err)
	}
	content := []mcp.Content{}
	if note != "" {
		content = append(content, &mcp.TextContent{Text: note})
	}
	content = append(content, &mcp.ImageContent{Data: png, MIMEType: "image/png"})
	return &mcp.CallToolResult{Content: content}
}

// ---- create-computer ----

type createComputerArgs struct {
	Workspace string   `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string   `json:"project"   jsonschema:"Project slug or name"`
	Env       string   `json:"env"       jsonschema:"Environment slug or name"`
	Name      string   `json:"name"      jsonschema:"Name for the machine, e.g. research-desktop"`
	StorageGB uint     `json:"storage_gb,omitempty" jsonschema:"Boot disk in GB, 20 to 200. Defaults to 20."`
	SSHKeys   []string `json:"ssh_authorized_keys,omitempty" jsonschema:"Optional SSH public keys for the ubuntu user. Not needed to use the machine: the screenshot, action and bash tools reach it without SSH."`
}

func handleCreateComputer(ctx context.Context, req *mcp.CallToolRequest, args createComputerArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	storage := args.StorageGB
	if storage == 0 {
		storage = 20
	}
	svcs := services(api, args.Workspace, args.Project, args.Env)
	svc, err := svcs.Create(ctx, cloud.CreateServiceInput{
		Name: args.Name,
		Type: cloud.ServiceTypeVM,
		VM: &cloud.VMInput{
			Image:             cloud.VMImageDesktop,
			StorageGB:         storage,
			SSHAuthorizedKeys: append([]string{}, args.SSHKeys...),
		},
	})
	if err != nil {
		return apiErr("create computer", err), nil, nil
	}
	if _, err := svcs.Deploy(ctx, svc.Slug, cloud.DeployOptions{AutoApproveChangeSets: true}); err != nil {
		return apiErr("start computer "+svc.Slug, err), nil, nil
	}
	return text(fmt.Sprintf(
		"Created desktop machine %q (service %s). It is booting; the desktop is usually ready in one to two minutes. "+
			"Until then computer-screenshot reports that the machine is not ready — wait and retry. "+
			"The screen is 1280x800. A person can watch it from the Desktop tab of this machine in the Simplifyd console.",
		svc.Name, svc.Slug)), nil, nil
}

// ---- computer-screenshot ----

func handleComputerScreenshot(ctx context.Context, req *mcp.CallToolRequest, args computerTarget) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	computer, err := computerFor(ctx, api, args)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	return screenshotResult(ctx, computer, ""), nil, nil
}

// ---- computer-action ----

type computerActionArgs struct {
	computerTarget
	Action     string `json:"action"  jsonschema:"One of: left_click, right_click, middle_click, double_click, mouse_move, left_click_drag, type, key, scroll, wait"`
	X          *int   `json:"x,omitempty" jsonschema:"X pixel (from the left) for clicks, mouse_move and scroll; the drag's end point for left_click_drag"`
	Y          *int   `json:"y,omitempty" jsonschema:"Y pixel (from the top) for clicks, mouse_move and scroll; the drag's end point for left_click_drag"`
	StartX     *int   `json:"start_x,omitempty" jsonschema:"Drag start X, for left_click_drag"`
	StartY     *int   `json:"start_y,omitempty" jsonschema:"Drag start Y, for left_click_drag"`
	Text       string `json:"text,omitempty" jsonschema:"Text to type, for type. Key name or chord in xdotool syntax for key, e.g. Return, ctrl+l, ctrl+shift+t, alt+Tab"`
	Direction  string `json:"direction,omitempty" jsonschema:"up, down, left or right, for scroll"`
	Amount     int    `json:"amount,omitempty" jsonschema:"Scroll notches (default 3), for scroll"`
	Seconds    int    `json:"seconds,omitempty" jsonschema:"How long to wait, up to 30, for wait (default 2)"`
	Screenshot *bool  `json:"screenshot,omitempty" jsonschema:"Return a screenshot after the action. Defaults to true."`
}

func handleComputerAction(ctx context.Context, req *mcp.CallToolRequest, args computerActionArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	computer, err := computerFor(ctx, api, args.computerTarget)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}

	point := func() (int, int, error) {
		if args.X == nil || args.Y == nil {
			return 0, 0, fmt.Errorf("%s needs x and y", args.Action)
		}
		return *args.X, *args.Y, nil
	}

	switch args.Action {
	case "left_click", "right_click", "middle_click", "double_click":
		x, y, perr := point()
		if perr != nil {
			return toolError(perr.Error()), nil, nil
		}
		switch args.Action {
		case "double_click":
			err = computer.DoubleClick(ctx, x, y)
		case "right_click":
			err = computer.Click(ctx, x, y, cloud.MouseRight)
		case "middle_click":
			err = computer.Click(ctx, x, y, cloud.MouseMiddle)
		default:
			err = computer.Click(ctx, x, y, cloud.MouseLeft)
		}
	case "mouse_move":
		x, y, perr := point()
		if perr != nil {
			return toolError(perr.Error()), nil, nil
		}
		err = computer.MouseMove(ctx, x, y)
	case "left_click_drag":
		x, y, perr := point()
		if perr != nil || args.StartX == nil || args.StartY == nil {
			return toolError("left_click_drag needs start_x, start_y, x and y"), nil, nil
		}
		err = computer.Drag(ctx, *args.StartX, *args.StartY, x, y)
	case "type":
		if args.Text == "" {
			return toolError("type needs text"), nil, nil
		}
		err = computer.Type(ctx, args.Text)
	case "key":
		if strings.TrimSpace(args.Text) == "" {
			return toolError("key needs text, e.g. Return or ctrl+l"), nil, nil
		}
		err = computer.Key(ctx, args.Text)
	case "scroll":
		x, y, perr := point()
		if perr != nil {
			return toolError(perr.Error()), nil, nil
		}
		direction := args.Direction
		if direction == "" {
			direction = "down"
		}
		amount := args.Amount
		if amount == 0 {
			amount = 3
		}
		err = computer.Scroll(ctx, x, y, cloud.ScrollDirection(direction), amount)
	case "wait":
		seconds := args.Seconds
		if seconds == 0 {
			seconds = 2
		}
		err = computer.Wait(ctx, time.Duration(seconds)*time.Second)
	default:
		return toolError(fmt.Sprintf("unknown action %q", args.Action)), nil, nil
	}
	if err != nil {
		return computerErr(args.Action, err), nil, nil
	}

	if args.Screenshot != nil && !*args.Screenshot {
		return text("Done."), nil, nil
	}
	if args.Action != "wait" {
		select {
		case <-time.After(screenshotSettle):
		case <-ctx.Done():
			return toolError(ctx.Err().Error()), nil, nil
		}
	}
	return screenshotResult(ctx, computer, ""), nil, nil
}

// ---- computer-bash ----

type computerBashArgs struct {
	computerTarget
	Command        string `json:"command" jsonschema:"Command to run with bash, as the ubuntu user in its home directory. DISPLAY is set, so GUI programs open on the screen; end those with & so the call returns, e.g. google-chrome https://example.com &"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"Seconds before the command is killed, up to 600. Defaults to 60."`
}

func handleComputerBash(ctx context.Context, req *mcp.CallToolRequest, args computerBashArgs) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	computer, err := computerFor(ctx, api, args.computerTarget)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	res, err := computer.Bash(ctx, args.Command, time.Duration(args.TimeoutSeconds)*time.Second)
	if err != nil {
		return computerErr("run command", err), nil, nil
	}

	var b strings.Builder
	if res.Truncated {
		b.WriteString("[output truncated; showing the last 1 MB]\n")
	}
	b.WriteString(res.Output)
	if res.TimedOut {
		b.WriteString("\n[killed: timed out]")
	} else {
		fmt.Fprintf(&b, "\n[exit code %d]", res.ExitCode)
	}
	result := text(b.String())
	result.IsError = res.TimedOut || res.ExitCode != 0
	return result, nil, nil
}

func RegisterComputerTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "create-computer",
		Description: "Create and start a desktop machine an AI agent can use as its computer: Ubuntu 24.04 with an Xfce desktop, Chrome and LibreOffice, on the project's private network. " +
			"Drive it with computer-screenshot, computer-action and computer-bash. Billed as a VM (2 vCPUs, 4 GB memory by default) while it runs.",
	}, handleCreateComputer)
	addTool(s, &mcp.Tool{
		Name: "computer-screenshot",
		Description: "Take a screenshot of a desktop machine's screen (1280x800). Coordinates in the image are the coordinates computer-action clicks. " +
			"Returns an error saying the machine is not ready while it is starting; wait a few seconds and retry.",
	}, handleComputerScreenshot)
	addTool(s, &mcp.Tool{
		Name: "computer-action",
		Description: "Use a desktop machine's mouse or keyboard, then return a screenshot of the result. " +
			"Actions: left_click, right_click, middle_click, double_click and mouse_move at (x, y); left_click_drag from (start_x, start_y) to (x, y); " +
			"type text; key presses a key or chord given in text (xdotool names: Return, Tab, Escape, BackSpace, ctrl+l, alt+F4); " +
			"scroll at (x, y) by amount in direction; wait seconds for something to load. Click a field before typing into it.",
	}, handleComputerAction)
	addTool(s, &mcp.Tool{
		Name: "computer-bash",
		Description: "Run a shell command on a desktop machine and return its output and exit code. " +
			"Faster and more reliable than the mouse for files, installs (sudo works) and anything scriptable; use computer-action for what only the screen can do.",
	}, handleComputerBash)
}
