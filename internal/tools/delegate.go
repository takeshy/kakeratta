package tools

import (
	"context"
	"encoding/json"
	"log"

	"github.com/takeshy/kakeratta/internal/mcp"
	"github.com/takeshy/kakeratta/internal/timeline"
)

// A built-in tool whose name an MCP server also offers goes to that server,
// with the post's position written into the arguments: the persona asks for
// "route" the same way either way. The built-in implementation stays as the
// fallback for when the server cannot be reached, so a deployment without the
// server, or with it down, answers as it did before.
//
// Only a failed call falls back. A server that answered with an error result
// (an overloaded map service, say) has said something the persona should see,
// and asking the same public service again from here would not change it.
type delegate struct {
	// params are the arguments the server's tool must accept. A same-named
	// tool that takes something else is a different tool and is left alone.
	params []string
	// args rewrites the persona's arguments for the server. false means the
	// built-in tool should answer, because it already explains what is missing.
	args func(args Args, at *timeline.Place) (Args, bool)
}

var delegates = map[string]delegate{
	"place_search": {params: []string{"query", "lat", "lng", "radius"}, args: placeSearchArgs},
	"route":        {params: []string{"to", "lat", "lng", "mode"}, args: routeArgs},
}

func placeSearchArgs(args Args, at *timeline.Place) (Args, bool) {
	query := args.str("query")
	if query == "" {
		return nil, false
	}
	forwarded := Args{"query": query}
	if at != nil {
		forwarded["lat"], forwarded["lng"] = at.Lat, at.Lon
	}
	if radius := args.num("radius", 0); radius > 0 && radius <= 5000 {
		forwarded["radius"] = radius
	}
	return forwarded, true
}

var routeModes = map[string]string{
	"": "driving", "driving": "driving", "car": "driving",
	"walking": "walking", "foot": "walking",
}

func routeArgs(args Args, at *timeline.Place) (Args, bool) {
	to := args.str("to")
	mode, known := routeModes[args.str("mode")]
	if to == "" || at == nil || !known {
		return nil, false
	}
	return Args{"to": to, "lat": at.Lat, "lng": at.Lon, "mode": mode}, true
}

// preferMCP returns tool, sent to the first listed server tool that can take
// its arguments, or unchanged when no server offers one.
func preferMCP(tool Tool, listed []mcp.Listed) Tool {
	d, ok := delegates[tool.Name]
	if !ok {
		return tool
	}
	for _, candidate := range listed {
		if candidate.Tool.Name != tool.Name || !accepts(candidate.InputSchema, d.params) {
			continue
		}
		name, qualified, builtin := tool.Name, candidate.QualifiedName(), tool.run
		tool.delegated = true
		tool.run = func(ctx context.Context, r *Registry, args Args, at *timeline.Place) (Result, error) {
			forwarded, ok := d.args(args, at)
			if !ok {
				return builtin(ctx, r, args, at)
			}
			result, err := r.config.MCP.Call(ctx, qualified, map[string]any(forwarded))
			if err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				log.Printf("tools: %s に繋がらないため内蔵の %s を使います: %v", qualified, name, err)
				return builtin(ctx, r, args, at)
			}
			return fromMCP(result)
		}
		return tool
	}
	return tool
}

func accepts(raw json.RawMessage, params []string) bool {
	var root schema
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	for _, name := range params {
		if _, ok := root.Properties[name]; !ok {
			return false
		}
	}
	return true
}
