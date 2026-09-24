import { Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { createCopy, type CreateVerb } from '@/lib/copy'
import { useNavigate } from '@/lib/router'

/**
 * The primary create action for a list page's `toolbarRight`: "New <Noun>",
 * or the verb of a resource that is added or started ("Add Member",
 * "Deploy Service"). The create page uses the same `createCopy(noun, verb)`
 * for its title, breadcrumb, and submit button.
 */
export function CreateButton({ noun, verb = 'New', to }: { noun: string; verb?: CreateVerb; to: string }) {
  const navigate = useNavigate()
  return (
    <Button size="sm" onClick={() => void navigate(to)}>
      <Plus />
      {createCopy(noun, verb).action}
    </Button>
  )
}
