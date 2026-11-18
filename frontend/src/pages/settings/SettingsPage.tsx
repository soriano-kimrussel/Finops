import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash2, UserPlus } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useMe } from '@/app/auth'
import { orgKey, useOrg } from '@/app/org'
import { ConfirmDialog, Dialog } from '@/components/Dialog'
import { Badge, Button, Card, CardHeader, ErrorState, Field, FormError, Input, PageHeader, Select, Spinner, Table, Td, Th, fieldError } from '@/components/ui'
import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { roleLabels, type Member, type Organization, type Role } from '@/lib/types'

const roles: Role[] = ['owner', 'admin', 'accountant', 'project_manager', 'viewer']

export function SettingsPage() {
  return (
    <>
      <PageHeader title="Settings" description="Organization profile and team access" />
      <div className="space-y-6">
        <OrgProfile />
        <Members />
      </div>
    </>
  )
}

function OrgProfile() {
  const { orgId, organization, can } = useOrg()
  const qc = useQueryClient()
  const [name, setName] = useState(organization.name)
  const [timezone, setTimezone] = useState(organization.timezone)
  const save = useMutation({
    mutationFn: () => api<{ organization: Organization }>(`/orgs/${orgId}`, { method: 'PATCH', body: { name, timezone } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['org', orgId] })
      qc.invalidateQueries({ queryKey: ['me'] })
    },
  })
  const editable = can('org.manage')
  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate()
  }

  return (
    <Card>
      <CardHeader title="Organization" description="Base currency is fixed once the organization is created." />
      <form onSubmit={submit} className="grid gap-4 p-5 sm:grid-cols-3">
        <Field label="Name" error={fieldError(save.error, 'name')}>
          {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} disabled={!editable} />}
        </Field>
        <Field label="Time zone" error={fieldError(save.error, 'timezone')}>
          {(p) => <Input {...p} value={timezone} onChange={(e) => setTimezone(e.target.value)} disabled={!editable} />}
        </Field>
        <Field label="Base currency">{(p) => <Input {...p} value={organization.base_currency} disabled />}</Field>
        {editable && (
          <div className="flex items-center gap-3 sm:col-span-3">
            <Button type="submit" loading={save.isPending}>
              Save changes
            </Button>
            {save.isSuccess && <span className="text-sm text-emerald-700">Saved</span>}
            {save.error && !fieldError(save.error, 'name') && !fieldError(save.error, 'timezone') ? <FormError error={save.error} /> : null}
          </div>
        )}
      </form>
    </Card>
  )
}

function Members() {
  const { orgId, can, role: myRole } = useOrg()
  const me = useMe()
  const qc = useQueryClient()
  const key = orgKey(orgId, 'members')
  const members = useQuery({ queryKey: key, queryFn: () => api<{ data: Member[] }>(`/orgs/${orgId}/members`) })
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<Member | null>(null)
  const manage = can('members.manage')

  const changeRole = useMutation({
    mutationFn: ({ id, role }: { id: string; role: Role }) => api(`/orgs/${orgId}/members/${id}`, { method: 'PATCH', body: { role } }),
    onSettled: () => qc.invalidateQueries({ queryKey: key }),
  })
  const remove = useMutation({
    mutationFn: (id: string) => api(`/orgs/${orgId}/members/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      setRemoving(null)
      qc.invalidateQueries({ queryKey: key })
    },
  })

  return (
    <Card>
      <CardHeader
        title="Members"
        description="Roles are enforced by the server for every request."
        action={
          manage && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <UserPlus className="size-4" aria-hidden /> Add member
            </Button>
          )
        }
      />
      {changeRole.error && (
        <div className="px-5 pt-4">
          <FormError error={changeRole.error} />
        </div>
      )}
      {members.isPending ? (
        <Spinner />
      ) : members.error ? (
        <ErrorState error={members.error} onRetry={() => members.refetch()} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>Name</Th>
              <Th>Email</Th>
              <Th>Role</Th>
              <Th>Joined</Th>
              <Th />
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {members.data.data.map((m) => {
              const isSelf = m.user_id === me.data?.user.id
              const canEdit = manage && !isSelf && (myRole === 'owner' || m.role !== 'owner')
              return (
                <tr key={m.id}>
                  <Td className="font-medium text-slate-900">
                    {m.name} {isSelf && <Badge>You</Badge>}
                  </Td>
                  <Td>{m.email}</Td>
                  <Td>
                    {canEdit ? (
                      <Select
                        aria-label={`Role for ${m.name}`}
                        className="w-44"
                        value={m.role}
                        onChange={(e) => changeRole.mutate({ id: m.id, role: e.target.value as Role })}
                      >
                        {roles
                          .filter((r) => myRole === 'owner' || r !== 'owner')
                          .map((r) => (
                            <option key={r} value={r}>
                              {roleLabels[r]}
                            </option>
                          ))}
                      </Select>
                    ) : (
                      <Badge tone={m.role === 'owner' ? 'purple' : 'gray'}>{roleLabels[m.role]}</Badge>
                    )}
                  </Td>
                  <Td>{formatDate(m.joined_at)}</Td>
                  <Td align="right">
                    {canEdit && (
                      <Button variant="ghost" size="sm" aria-label={`Remove ${m.name}`} onClick={() => setRemoving(m)}>
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </Td>
                </tr>
              )
            })}
          </tbody>
        </Table>
      )}
      <AddMemberDialog open={adding} onOpenChange={setAdding} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(o) => !o && setRemoving(null)}
        title="Remove member"
        description={removing ? `${removing.name} will immediately lose access to this organization.` : undefined}
        confirmLabel="Remove"
        tone="danger"
        loading={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing.id)}
      >
        <FormError error={remove.error} />
      </ConfirmDialog>
    </Card>
  )
}

function AddMemberDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { orgId, role: myRole } = useOrg()
  const qc = useQueryClient()
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const add = useMutation({
    mutationFn: () => api(`/orgs/${orgId}/members`, { method: 'POST', body: { email, role } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'members') })
      setEmail('')
      onOpenChange(false)
    },
  })
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Add member"
      description="The person must already have a FinOps HQ account."
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => add.mutate()} loading={add.isPending}>
            Add member
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {add.error && !fieldError(add.error, 'email') ? <FormError error={add.error} /> : null}
        <Field label="Email" error={fieldError(add.error, 'email')}>
          {(p) => <Input {...p} type="email" value={email} onChange={(e) => setEmail(e.target.value)} />}
        </Field>
        <Field label="Role">
          {(p) => (
            <Select {...p} value={role} onChange={(e) => setRole(e.target.value as Role)}>
              {roles
                .filter((r) => myRole === 'owner' || r !== 'owner')
                .map((r) => (
                  <option key={r} value={r}>
                    {roleLabels[r]}
                  </option>
                ))}
            </Select>
          )}
        </Field>
      </div>
    </Dialog>
  )
}
