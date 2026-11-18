# Artel

<!-- impeccable:product-schema 1 -->

## Platform

Native Linux desktop on Omarchy, implemented in Qt Quick/QML and C++17. This is not a web view, Android or iOS app. Impeccable's documented platform enum does not describe native Qt desktop; use native desktop review and rendering, not browser DOM scans or mobile-platform rules. Future macOS portability is an architectural boundary, not a delivered platform.

## Users

The owner is the primary user: a developer managing a personal AI development team and several projects from a local computer. Agents work in Pi and their terminals live primarily in agterm. Other product audiences have not been established.

## Product Purpose

A persistent control panel for projects, tasks, queues and observation of an AI development team. The owner explicitly wants to see **the entire team immediately on opening**, not land inside the most recent project's task editor. The overview should make current observations and loss of visibility understandable; projects lead to the manual task workspace.

The long-term intent includes communication, owner decisions and an end-to-end development cycle. Those are not delivered in the current application and must not appear as working features in the redesign.

## Positioning

Artel combines owner-managed persistent project/task state with explicitly connected Pi observations. It complements rather than replaces agterm. It distinguishes reported activity, connection freshness and owner manual task state; a terminal event is not proof of completed work.

## Operating Context

- Local Go API/CLI and SQLite, independent native desktop process; closing the desktop does not stop the daemon.
- The owner has manually checked the current interface and left the server running. Design preparation must not stop/restart that process, mutate its data or connect a real agent without separate permission.
- Native keyboard/mouse workflows, Russian and English UI, Omarchy accent/language/rounding integration. No global desktop settings changes.
- Projects represent persistent working directories/service groups, not individual features. The future team includes Lead, Product, developers and reviewers, all using Pi; observations currently do not assign these roles.

## Capabilities and Constraints

Implemented: manual project/task create/edit/archive, criteria, ordered queues, explicit manual state transitions, audit history, concurrent-edit conflict handling, uncertain-request recovery; opt-in Pi registration/candidates, explicit project/runtime binding, activity and freshness, revocation/pruning, best-effort navigation to a claimed agterm session.

Not implemented: prompt delivery, task assignment to an agent, agent launch/control, automated queue execution, approval inbox, MR/CI orchestration, remote access, Android, macOS implementation. Do not invent timelines, token budgets, productivity metrics or completed-task evidence from telemetry.

Observer registrations are not the complete logical team roster: an agent can have multiple runtime candidates, and a runtime can be superseded or revoked. Unbound candidates must not inflate the number of working team members. No heartbeat means lost observation, not permission to start a replacement writer. Idle is not task acceptance. Manual task states remain visibly manual.

Desktop logic/networking stays in C++; QML handles presentation/actions. Keep the private owner credential out of QML, preserve version/CAS/idempotency and all draft/unknown-outcome protections. No new backend orchestration as a side effect of redesign.

## Brand Commitments

Name: Artel. User requests a substantial improvement over the current plain, unattractive technical prototype, using Impeccable. For this desktop redesign the owner selected **canon**: a familiar, polished desktop dashboard rather than an expressive borrowed visual world. The chosen composition is the neutral dashboard concept in `docs/design/reference/canon-team-overview.html`: whole-team overview, conventional navigation/filters, readable roster and selected-observer details. The owner likes **Zed Editor** as a craft reference and delegated other reference choices. Borrow its calm density, clear active surfaces, thin separators and keyboard-oriented polish, not its code-editor layout, agent execution features or typography for code. The chosen canon composition remains authoritative. Final tokens and component styling are implementation details to verify in native renders; no preference for novel metaphors is implied.

## Evidence on Hand

- README.md and docs/development.md describe the delivered contract and local operation.
- docs/architecture/ describes the future product, with scope explicitly wider than the implementation.
- desktop/src/Main.qml and ObserverPanel.qml are incumbent implementation evidence, not a visual style to preserve.
- docs/screenshots/manual-panel*.png are historical fictional fixtures. Fresh fixture captures on 2026-11-18 confirm the three-column prototype; they are not screenshots of owner data or approval of a replacement design.
- Owner reports successful manual UI verification. Local automated Go/Qt/SDK/PTY/e2e/package checks passed. Remote native CI is paused by owner decision for the local pilot; Go CI remains enabled. Do not convert these facts into an external product claim.

## Product Principles

- Whole-team visibility first; project/task detail is a deliberate next step.
- Show known facts and uncertainty without inventing agent progress or team capabilities.
- Protect the owner's text and decisions through reconnect, conflict and unknown outcomes.
- Observation and navigation never secretly execute work.
- Preserve native usability and local ownership of data.

## Accessibility & Inclusion

Preserve keyboard navigation, visible focus, accessible names and standard Save/Cancel/Quit shortcuts. Never rely on color alone for activity, freshness, errors or manual state. Accommodate long Russian/English text, non-BMP metadata and larger font/display scaling without hiding critical controls.
