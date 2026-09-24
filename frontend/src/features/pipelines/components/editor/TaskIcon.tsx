import { BookOpen, Code2, FileCode2 } from 'lucide-react'
import type { PipelineTaskType } from '../../editor'

export function TaskIcon({ type }: { type: PipelineTaskType }) {
  const Icon = type === 'notebook' ? BookOpen : type === 'python' ? Code2 : FileCode2
  return <Icon size={16} className="text-muted-foreground" />
}
