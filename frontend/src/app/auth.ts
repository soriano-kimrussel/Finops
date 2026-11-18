import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '@/lib/api'
import type { Me } from '@/lib/types'

export const meKey = ['me'] as const

export function useMe() {
  return useQuery({
    queryKey: meKey,
    queryFn: async () => {
      try {
        return await api<Me>('/auth/me')
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null
        throw err
      }
    },
    staleTime: 60_000,
    retry: false,
  })
}

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { email: string; password: string }) => api('/auth/login', { method: 'POST', body }),
    onSuccess: async () => {
      qc.clear()
      await qc.fetchQuery({ queryKey: meKey, queryFn: () => api<Me>('/auth/me') })
    },
  })
}

export function useRegister() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string; email: string; password: string }) => api('/auth/register', { method: 'POST', body }),
    onSuccess: async () => {
      qc.clear()
      await qc.fetchQuery({ queryKey: meKey, queryFn: () => api<Me>('/auth/me') })
    },
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api('/auth/logout', { method: 'POST' }),
    onSettled: () => {
      qc.clear()
      qc.setQueryData(meKey, null)
    },
  })
}
