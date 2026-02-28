# Frontend Integration Design

## Stack
- React Query (TanStack) for data fetching + mutations
- SSE for real-time updates
- GitHub OAuth for user-to-installation linking
- Supermemory dual-layer scoping (repo + org)

## Pages
| Page | Endpoints | Features |
|------|-----------|----------|
| Dashboard | GET /stats, GET /activity | Live stats, activity feed, SSE auto-refresh |
| Repos | GET /repos, PATCH /repos/:id | List, toggle enabled, edit branch |
| Reviews | GET /repos/:id/reviews, GET /reviews/:id | Browse, drill into comments, retry |
| Rules | CRUD /rules | Full CRUD, priority ordering |
| Settings | GET/PUT/DELETE /repos/:id/config/:stage | Per-repo model config |

## Auth Flow
Clerk JWT → Bearer token → Go backend JWKS validation

## User Onboarding
1. Sign in via Clerk
2. "Connect GitHub" → OAuth flow
3. Store clerk_user_id → github_user_id in user_links table
4. Match GitHub org membership to installations
5. Filter repos by user's linked installations

## Memory Scoping
| Layer | Tag | Content | Purpose |
|-------|-----|---------|---------|
| Repo | repo:<owner/repo>:reviews | Past comments + diffs | File-specific patterns |
| Org | org:<owner>:patterns | Cross-repo patterns | Team-wide anti-patterns |
| Rules | org:rules | Manual rules | Org standards |

## SSE
- Endpoint: GET /api/v1/events
- Events: review_started, review_completed, review_failed
- Frontend: invalidates React Query caches on events
