import { useSyncExternalStore } from 'react'

// The app theme is the `dark` class on <html> (set by the theme toggle and by
// index.html before first paint). Components that need the theme as a value —
// third-party widgets with their own color-mode prop, like React Flow — read
// it here instead of hard-coding one mode.
function subscribe(onChange: () => void) {
  const observer = new MutationObserver(onChange)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  return () => observer.disconnect()
}

function isDark() {
  return document.documentElement.classList.contains('dark')
}

export function useIsDarkTheme(): boolean {
  return useSyncExternalStore(subscribe, isDark, () => false)
}
