package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// ---- set-bucket-public-access ----

type setBucketPublicAccessArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Bucket service slug or name"`
	Public    bool   `json:"public"    jsonschema:"true lets anyone read the bucket's files without credentials; false requires signed requests again"`
}

func handleSetBucketPublicAccess(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args setBucketPublicAccessArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	b, err := services(api, args.Workspace, args.Project, args.Env).Bucket(args.Service).SetPublicAccess(ctx, args.Public)
	if err != nil {
		return apiErr("set bucket public access", err), nil, nil
	}
	return jsonText(bucketView(b)), nil, nil
}

// ---- add-bucket-domain / remove-bucket-domain ----

type bucketDomainArgs struct {
	Workspace string `json:"workspace" jsonschema:"Workspace slug or name"`
	Project   string `json:"project"   jsonschema:"Project slug or name"`
	Env       string `json:"env"       jsonschema:"Environment slug or name"`
	Service   string `json:"service"   jsonschema:"Bucket service slug or name"`
	Domain    string `json:"domain"    jsonschema:"The custom domain, e.g. cdn.example.com"`
}

func handleAddBucketDomain(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args bucketDomainArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	names := requestedDomains(args.Domain, nil)
	if len(names) == 0 {
		return text("domain is required"), nil, nil
	}
	svcs := services(api, args.Workspace, args.Project, args.Env)
	b, err := svcs.Bucket(args.Service).AddDomain(ctx, names[0])
	if err != nil {
		return apiErr("add bucket domain", err), nil, nil
	}
	// The domain's routing only takes effect on the bucket's next deploy.
	if _, err := svcs.Deploy(ctx, args.Service, cloud.DeployOptions{AutoApproveChangeSets: true}); err != nil {
		return apiErr("deploy bucket", err), nil, nil
	}
	out := bucketView(b)
	if d := b.Domain(names[0]); d != nil {
		meanwhile := ""
		if b.PublicURL != "" {
			meanwhile = "the files are served on " + b.PublicURL
		}
		out["added"] = domainStep{
			Domain: d.Domain, CNAMETarget: b.DomainCNAMETarget, Zone: d.DNSZone, DNSError: d.DNSError,
			Verification: d.Verification, Meanwhile: meanwhile,
		}.summary(d.Status)
	}
	return jsonText(out), nil, nil
}

func handleRemoveBucketDomain(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args bucketDomainArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	names := requestedDomains(args.Domain, nil)
	if len(names) == 0 {
		return text("domain is required"), nil, nil
	}
	svcs := services(api, args.Workspace, args.Project, args.Env)
	b, err := svcs.Bucket(args.Service).RemoveDomain(ctx, names[0])
	if err != nil {
		return apiErr("remove bucket domain", err), nil, nil
	}
	if _, err := svcs.Deploy(ctx, args.Service, cloud.DeployOptions{AutoApproveChangeSets: true}); err != nil {
		return apiErr("deploy bucket", err), nil, nil
	}
	out := bucketView(b)
	out["removed"] = names[0]
	return jsonText(out), nil, nil
}

// bucketView is what the bucket tools return: whether the bucket is public,
// where, and its custom domains with their verification. Credentials and
// usage are left to get-service.
func bucketView(b *cloud.S3Bucket) map[string]any {
	return map[string]any{
		"bucket_name":         b.BucketName,
		"public":              b.Public,
		"public_url":          b.PublicURL,
		"custom_domains":      b.CustomDomains,
		"domain_cname_target": b.DomainCNAMETarget,
	}
}

// RegisterBucketTools adds the bucket public-access and domain tools.
func RegisterBucketTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "set-bucket-public-access",
		Description: "Turn anonymous (unauthenticated) reads of an S3 bucket service's files on or off. A public " +
			"bucket serves every file at its public_url, and on its custom domains, to anyone. Uploads and other " +
			"signed requests are unaffected. A bucket with custom domains cannot be made private until they are " +
			"removed. Confirm with the user before making a bucket public.",
	}, handleSetBucketPublicAccess)

	addTool(s, &mcp.Tool{
		Name: "add-bucket-domain",
		Description: "Serve a public S3 bucket's files on a custom domain, such as cdn.example.com, alongside its " +
			"public URL and any other domains (up to 20). The bucket must be public (set-bucket-public-access). " +
			"Only reads are served on the domain. Deploys the bucket so routing takes effect. The domain is not " +
			"served until it is verified: the result's next_step gives the TXT record to publish and the CNAME " +
			"target, unless the domain is in a zone the workspace hosts here, when its records are added " +
			"automatically. Pass next_step on to the user, then call verify-domain once the record is published.",
	}, handleAddBucketDomain)

	addTool(s, &mcp.Tool{
		Name: "remove-bucket-domain",
		Description: "Stop serving an S3 bucket's files on one of its custom domains, named by the domain or its " +
			"slug. The bucket keeps serving on its public URL and other domains. Deploys the bucket so routing " +
			"takes effect.",
	}, handleRemoveBucketDomain)
}
