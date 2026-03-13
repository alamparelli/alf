# System Skills (skills.d/)

These skills are bundled with ALF and managed by the system. They are updated automatically on each upgrade.

**Do not edit files in this directory** - your changes will be overwritten on the next update.

## User-editable files

The following files are read by skills at runtime and are **never overwritten** by ALF:

| File | Location | Purpose |
|------|----------|---------|
| `heartbeat.md` | `context/heartbeat.md` | Custom instructions for the heartbeat job. Edit the body to define what the heartbeat should do. Leave empty to skip. |

## Creating your own skills

To create custom skills, use the `data/skills/` directory instead. Skills placed there are user-managed and will not be touched by upgrades.

See the [Creating Skills](docs/creating-skills.md) documentation in the Control Center for details.
