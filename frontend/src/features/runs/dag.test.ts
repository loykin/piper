import { describe, expect, it } from 'vitest'
import { parseStepsFromYaml } from './dag'

describe('parseStepsFromYaml', () => {
  // outputs and env entries also start with "- name:"; only spec.steps are nodes.
  it('reads only spec.steps, not outputs or env entries', () => {
    const yaml = `spec:
  steps:
    - name: train
      options:
        env:
          - name: PYTHONPATH
            value: .
      outputs:
        - name: model
          path: model.pkl
    - name: eval
      depends_on: [train]
`
    expect(parseStepsFromYaml(yaml)).toEqual([
      { name: 'train', depends_on: [] },
      { name: 'eval', depends_on: ['train'] },
    ])
  })
})
