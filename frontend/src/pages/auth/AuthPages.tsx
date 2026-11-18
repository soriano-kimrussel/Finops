import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { Link, Navigate, useLocation, useNavigate } from 'react-router'
import { z } from 'zod'
import { useLogin, useMe, useRegister } from '@/app/auth'
import { Button, Card, Field, FormError, Input, Spinner, fieldError } from '@/components/ui'
import type { ReactNode } from 'react'

function AuthShell({ title, subtitle, children, footer }: { title: string; subtitle: string; children: ReactNode; footer: ReactNode }) {
  return (
    <div className="flex min-h-full items-center justify-center bg-gradient-to-b from-slate-50 to-slate-100 px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center text-center">
          <img src="/favicon.svg" alt="" className="mb-4 size-10" />
          <h1 className="text-xl font-semibold tracking-tight text-slate-900">{title}</h1>
          <p className="mt-1 text-sm text-slate-500">{subtitle}</p>
        </div>
        <Card className="p-6">{children}</Card>
        <p className="mt-6 text-center text-sm text-slate-500">{footer}</p>
      </div>
    </div>
  )
}

export const loginSchema = z.object({
  email: z.string().trim().min(1, 'Enter your email').email('Enter a valid email address'),
  password: z.string().min(1, 'Enter your password'),
})

type LoginValues = z.infer<typeof loginSchema>

export function LoginPage() {
  const me = useMe()
  const login = useLogin()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const form = useForm<LoginValues>({ resolver: zodResolver(loginSchema), defaultValues: { email: '', password: '' } })

  if (me.isPending) return <Spinner />
  if (me.data) return <Navigate to={from} replace />

  const onSubmit = form.handleSubmit((values) => login.mutate(values, { onSuccess: () => navigate(from, { replace: true }) }))

  return (
    <AuthShell
      title="Sign in to FinOps HQ"
      subtitle="Financial operations for your organizations"
      footer={
        <>
          New here?{' '}
          <Link to="/register" className="font-medium text-brand-600 hover:text-brand-700">
            Create an account
          </Link>
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4" noValidate>
        <FormError error={login.error} />
        <Field label="Email" error={form.formState.errors.email?.message}>
          {(p) => <Input {...p} type="email" autoComplete="email" {...form.register('email')} />}
        </Field>
        <Field label="Password" error={form.formState.errors.password?.message}>
          {(p) => <Input {...p} type="password" autoComplete="current-password" {...form.register('password')} />}
        </Field>
        <Button type="submit" className="w-full" loading={login.isPending}>
          Sign in
        </Button>
      </form>
    </AuthShell>
  )
}

export const registerSchema = z.object({
  name: z.string().trim().min(1, 'Enter your name').max(200),
  email: z.string().trim().min(1, 'Enter your email').email('Enter a valid email address'),
  password: z.string().min(12, 'Use at least 12 characters').max(128, 'Use at most 128 characters'),
})

type RegisterValues = z.infer<typeof registerSchema>

export function RegisterPage() {
  const me = useMe()
  const register = useRegister()
  const navigate = useNavigate()
  const form = useForm<RegisterValues>({ resolver: zodResolver(registerSchema), defaultValues: { name: '', email: '', password: '' } })

  if (me.isPending) return <Spinner />
  if (me.data) return <Navigate to="/" replace />

  const onSubmit = form.handleSubmit((values) => register.mutate(values, { onSuccess: () => navigate('/orgs/new', { replace: true }) }))
  const errors = form.formState.errors

  return (
    <AuthShell
      title="Create your account"
      subtitle="You can create or join organizations next"
      footer={
        <>
          Already have an account?{' '}
          <Link to="/login" className="font-medium text-brand-600 hover:text-brand-700">
            Sign in
          </Link>
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4" noValidate>
        <FormError error={register.error} />
        <Field label="Full name" error={errors.name?.message ?? fieldError(register.error, 'name')}>
          {(p) => <Input {...p} autoComplete="name" {...form.register('name')} />}
        </Field>
        <Field label="Work email" error={errors.email?.message ?? fieldError(register.error, 'email')}>
          {(p) => <Input {...p} type="email" autoComplete="email" {...form.register('email')} />}
        </Field>
        <Field label="Password" hint="At least 12 characters" error={errors.password?.message ?? fieldError(register.error, 'password')}>
          {(p) => <Input {...p} type="password" autoComplete="new-password" {...form.register('password')} />}
        </Field>
        <Button type="submit" className="w-full" loading={register.isPending}>
          Create account
        </Button>
      </form>
    </AuthShell>
  )
}
