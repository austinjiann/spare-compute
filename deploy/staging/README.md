# Staging deployment

The first hosted environment runs one instance of the payload-opaque
rendezvous service on Railway. Railway builds
`deploy/connectivity/Dockerfile`, terminates public TLS, supplies `PORT`, and
checks `/healthz` before activating a deployment. `railway.json` is the source
of truth for those settings.

This deployment does not make remote jobs work yet. It gives the upcoming
daemon client a real HTTPS rendezvous endpoint. STUN and TURN require public
UDP and a TURN relay port range, so they will run on a small public VPS in a
separate slice rather than inside this Railway service.

## Important constraints

- Keep exactly one rendezvous replica. Its bounded routes and signals are held
  in process memory, so ordinary load balancing would split paired endpoints.
- Do not attach a database or volume. Restarting safely discards only expiring
  presence and signaling state; clients must republish it.
- Treat this as staging, not production. The service has bounded payload,
  route, queue, and per-route request limits, but production still needs
  edge-level abuse protection and multiple failure-isolated locations.
- Do not add request-body or authorization-header logging. Hosted traffic is
  opaque, but route tokens are bearer credentials.

## First deployment

Only deploy a commit that has landed on `main`:

```bash
git switch main
git pull --ff-only
railway whoami
railway up --new --name computehop-connectivity-staging
```

The final command creates a Railway project and service, links this checkout,
builds the custom Dockerfile, and deploys it. It can create billable resources,
so confirm the intended Railway workspace and plan in the prompt.

Generate a Railway HTTPS domain and verify the live process:

```bash
railway domain
railway domain list
connectivity_url="https://replace-with-the-generated-domain"
curl --fail --show-error "$connectivity_url/healthz"
```

The response must be exactly `ok`. Keep the generated URL in staging client
configuration; it is public routing information, not a secret.

To deploy automatically after changes land on `main`, connect the linked
service to GitHub:

```bash
railway service source connect --repo austinjiann/spare-compute --branch main
```

The watch patterns in `railway.json` limit automatic builds to files that can
change the rendezvous binary or image.

## Routine rollout and checks

For a manual rollout from an already linked checkout:

```bash
railway up --detach --message "deploy rendezvous"
railway deployment list
railway logs --latest --lines 100
```

After Railway reports the deployment as successful, call `/healthz` again and
exercise an authenticated presence/signal round trip before using that build
for client testing. Railway's health check gates deployment activation; it is
not continuous uptime monitoring, so add an external probe before launch.

If a rollout is bad, use the Railway deployment view to redeploy the previous
successful image. If that image is no longer retained, check out the previous
known-good commit and upload it with `railway up`.

## Expected cost

As of July 2026, Railway Free includes a small monthly resource credit suitable
for early experiments. Hobby has a $5 monthly minimum that includes $5 of
usage; CPU, memory, and egress above that are metered. Check the current
[Railway pricing documentation](https://docs.railway.com/pricing/plans) before
creating the project.

The later TURN relay is bandwidth-sensitive and is not covered by this service.
A small VPS currently starts around $4 per month; a 1 GiB instance around $6 is
a safer staging default. Relay egress and abuse, not rendezvous CPU, will become
the main variable cost.
