import { useOrg } from '@/app/org'
import { PageHeader } from '@/components/ui'

export function OverviewPage() {
  const { organization } = useOrg()
  return <PageHeader title="Overview" description={`Financial summary for ${organization.name}`} />
}
