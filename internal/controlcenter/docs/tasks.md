---
category: Features
tags: tasks, agents, orchestration, approval, teams
order: 5
---

# Tasks

Tasks are background agent jobs that run independently from chat. They execute in their own goroutine, tracked by the Orchestrator, and multiple tasks can run concurrently.

## Launching a task

1. Open the **Tasks** tab in the sidebar.
2. Click **+ Add Task** to reveal the launcher.
3. Write a prompt describing what the agent should do.
4. Optionally select a **Team** from the dropdown (only shown if teams are configured).
5. Toggle **Require approval before execution** if you want to review the agent's plan before it runs.
6. Click **Launch**.

The task starts immediately in the background. You can close the tab and come back later.

## Task states

| Status | Meaning |
|--------|---------|
| `running` | Task is actively executing |
| `completed` | Task finished successfully |
| `failed` | Task encountered an error |
| `timeout` | Task exceeded its time limit |
| `interrupted` | Task was running when the daemon restarted (orphaned) |
| `awaiting_approval` | Agent produced a plan and is waiting for your approval |
| `awaiting_arbitration` | Agent needs human input to resolve a conflict or ambiguity |

## Monitoring tasks

The task list is split into two sections:

- **Running** -- always visible, shows active tasks with live metadata.
- **Completed** -- collapsed by default, click to expand. Shows the last 20 finished tasks sorted by start time.

Each task card displays:

- **Short ID** -- a monospace `#xxxxxxxx` badge (first 8 characters of the task ID) for quick reference.
- **Status badge** -- color-coded by state.
- **Cost** -- cumulative USD cost of all agent calls.
- **Elapsed time** -- how long the task has been running.
- **Team name** -- if the task was assigned to a team.
- **Iteration count** -- number of orchestrator iterations so far.

Click a task card to expand it and see:

- The full original prompt.
- The agent's **Plan** (numbered steps with assigned agents, if available).
- **Agent Steps** -- each sub-agent call with its status, cost, task description, output (rendered as markdown), and any errors. Click a step to expand/collapse its output.
- **Questions** the agent raised during execution.
- **Final Output** (completed tasks only) -- the agent's response rendered as markdown.

Use the **Refresh** button to manually reload task state. Tasks also update automatically via server-sent events.

## Approval flow

When you launch a task with **Require approval** enabled, the task pauses at `awaiting_approval` or `awaiting_arbitration` after the agent produces its plan.

The approval UI appears inside the expanded task card:

1. Review the plan and agent steps.
2. Optionally write feedback in the text area.
3. Click **Approve** to let the agent proceed, or **Reject** to stop it with your feedback.

## Cancelling a running task

Click the **Stop** button on a running task card. A confirmation dialog appears before cancellation.

## Restarting a task

Completed, failed, and interrupted tasks show a **Restart** button. Clicking it reveals a text area where you can add an optional comment (e.g., "try a different approach"). The comment is prepended to the original prompt as a restart note. The restarted task inherits the original team assignment.

## Deleting completed tasks

Click the **Delete** button on any completed task card. A confirmation dialog appears before deletion. This removes the task from disk permanently.

## Desktop notifications

ALF requests browser notification permission when you open the Tasks tab. When a running task reaches a terminal state (completed, failed, timeout), a desktop notification is sent with the task status and a truncated prompt preview.

## Relationship to Teams

If you have agent teams configured, the task launcher shows a **Team** dropdown. Selecting a team routes the task to that team's multi-agent orchestration pipeline instead of a single agent.

See [Agent Teams](agent-teams.md) for team configuration details.

## What's next

- [Agent Teams](agent-teams.md) -- configure multi-agent teams for complex tasks
- [Setting Up Tiers](tier-setup.md) -- the `agent` tier controls model, effort, and timeout for tasks
- [Schedules](schedules.md) -- automate recurring tasks on a cron schedule
