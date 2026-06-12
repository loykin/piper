/* eslint-disable react-refresh/only-export-components */
import { createContext, useContext, useState, useEffect, type ReactNode } from 'react'
import {
  capabilities as loadCapabilities,
  me,
  logout as apiLogout,
  refresh,
  type AuthCapabilities,
  type AuthUser,
} from './api'
import { configureAuthRefresh } from '@/lib/api'

interface AuthContextValue {
  user: AuthUser | null
  capabilities: AuthCapabilities | null
  loading: boolean
  logout: () => Promise<void>
  setUser: (u: AuthUser | null) => void
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  capabilities: null,
  loading: true,
  logout: async () => {},
  setUser: () => {},
})

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [capabilities, setCapabilities] = useState<AuthCapabilities | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadCapabilities()
      .then(async (caps) => {
        setCapabilities(caps)
        configureAuthRefresh(caps.login_mode === 'password')
        if (!caps.authentication || !caps.login_routes) {
          return
        }
        const u = await me()
        if (u) {
          setUser(u)
        } else if (caps.login_mode === 'password') {
          const refreshed = await refresh()
          setUser(refreshed)
        }
      })
      .catch(() => setUser(null))
      .finally(() => setLoading(false))
  }, [])

  const logout = async () => {
    if (capabilities?.login_routes) {
      await apiLogout()
    }
    setUser(null)
  }

  return (
    <AuthContext.Provider value={{ user, capabilities, loading, logout, setUser }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}
