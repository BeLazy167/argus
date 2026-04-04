# Create Argus README.md

## Objective
Create a comprehensive, professional README.md file for the Argus AI code review bot that explains what it does, how to use it, and how to self-host it.

## Research Summary
From exploring the codebase:
- **Argus** is an AI-powered code review GitHub App
- **Key features**: Multi-stage pipeline, specialist AI reviewers, semantic memory, incremental reviews, bot commands
- **Stack**: Go backend (Chi, PostgreSQL, Ent), Next.js frontend, Supermemory for RAG
- **License**: Sustainable Use License (like n8n) - free for internal/non-commercial use
- **Self-hostable**: Yes, with clear setup instructions

## README Structure

### 1. Header Section
- Project name and tagline
- Badges (optional placeholders)
- One-line description of value proposition

### 2. What is Argus?
- 2-3 paragraph explanation
- Key differentiators from other code review tools
- Target audience

### 3. Key Features
- Bullet points with emojis for key capabilities
- Focus on: AI review, memory/learning, cross-file analysis, bot commands

### 4. Quick Start (For Users)
- How to install the GitHub App (hosted version)
- Basic usage (it just works on PRs)
- Bot commands reference

### 5. Self-Hosting Guide
- Prerequisites (Go 1.24+, PostgreSQL, GitHub App setup)
- Environment variables table
- Step-by-step setup
- Docker option
- Database migrations

### 6. Architecture Overview
- Brief description of the pipeline
- Key components
- Technology stack

### 7. Configuration
- Required env vars
- Optional env vars
- LLM provider setup

### 8. Development
- How to run locally
- Makefile commands
- Testing

### 9. Bot Commands Reference
- Table of all commands
- Usage examples

### 10. License
- Explain Sustainable Use License
- Link to LICENSE file
- What is/isn't allowed

### 11. Support & Community
- How to get help
- Links to docs

## Verification Criteria
- [ ] README is comprehensive and professional
- [ ] All major features are documented
- [ ] Self-hosting instructions are clear and complete
- [ ] Environment variables are documented
- [ ] Bot commands are explained
- [ ] License is clearly explained
- [ ] Links work (internal references)

## Potential Risks
- Information overload - keep it scannable with clear sections
- Missing important setup steps - reference actual config files
- License confusion - be very clear about Sustainable Use License terms