import * as icons from 'lucide-svelte'

// Map icon name strings (e.g. "package", "calendar") to Svelte components
const iconMap: Record<string, any> = {}

// Build map from lucide exports (PascalCase → kebab-case)
for (const [key, component] of Object.entries(icons)) {
  if (typeof component === 'function' || (typeof component === 'object' && component !== null)) {
    // Convert PascalCase to kebab-case: "CalendarClock" → "calendar-clock"
    const kebab = key
      .replace(/([a-z])([A-Z])/g, '$1-$2')
      .replace(/([A-Z])([A-Z][a-z])/g, '$1-$2')
      .toLowerCase()
    iconMap[kebab] = component
    // Also map lowercase without dashes for common usage
    iconMap[key.toLowerCase()] = component
  }
}

/**
 * Get a Lucide icon component by name string.
 * Returns undefined if not found.
 */
export function getIcon(name: string): any {
  if (!name) return undefined
  return iconMap[name] || iconMap[name.toLowerCase()] || undefined
}
