import { useTheme } from '../lib/theme'
import { IconMoon, IconSun } from './Icons'

/** Light / dark switch. Used on the login screen. */
export default function ThemeToggle({ className = '' }: { className?: string }) {
  const { theme, toggleTheme } = useTheme()
  const dark = theme === 'dark'
  return (
    <button
      type="button"
      className={`theme-toggle ${className}`.trim()}
      onClick={toggleTheme}
      aria-label={dark ? 'Switch to light mode' : 'Switch to dark mode'}
      title={dark ? 'Light mode' : 'Dark mode'}
    >
      {dark ? <IconSun width={18} height={18} /> : <IconMoon width={18} height={18} />}
      <span>{dark ? 'Light' : 'Dark'}</span>
    </button>
  )
}
