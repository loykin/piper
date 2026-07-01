import{n as e}from"./rolldown-runtime.js";import{o as t,s as n}from"./vendor-codemirror.js";import{a as r}from"./vendor-loykin.js";import{C as i,S as a}from"./index.js";import{t as o}from"./yaml-mirror.js";import{n as s}from"./hooks2.js";var c=e(n(),1),l=t(),u=[{label:`GPU (NVIDIA)`,description:`Schedule notebooks on NVIDIA GPU nodes with the nvidia runtime.`,yaml:`spec:
  nodeSelector:
    nvidia.com/gpu: "true"
  tolerations:
    - key: dedicated
      operator: Equal
      value: gpu
      effect: NoSchedule
  runtimeClassName: nvidia
`},{label:`Spot instances`,description:`Allow notebooks to land on spot/preemptible nodes.`,yaml:`spec:
  tolerations:
    - key: cloud.google.com/gke-spot
      operator: Exists
      effect: NoSchedule
`},{label:`FUSE device`,description:`Mount the host /dev/fuse device for FUSE-based filesystems.`,yaml:`spec:
  volumes:
    - name: fuse
      hostPath:
        path: /dev/fuse
  containers:
    - name: notebook
      securityContext:
        privileged: false
        capabilities:
          add: [SYS_ADMIN]
      volumeMounts:
        - name: fuse
          mountPath: /dev/fuse
`},{label:`Service account`,description:`Attach a Kubernetes service account to all notebooks on this worker.`,yaml:`spec:
  serviceAccountName: notebook-runner
`}];function d({workerId:e,initialYaml:t,onSaved:n,onCancel:d}){let f=!!e,[p,m]=(0,c.useState)(e??``),[h,g]=(0,c.useState)(t??``),[_,v]=(0,c.useState)(``),{mutateAsync:y,isPending:b}=s();async function x(){v(``);let e=p.trim();if(!e){v(`Worker ID is required.`);return}if(!h.trim()){v(`Pod template YAML is required.`);return}try{await y({workerId:e,yaml:h}),n(e)}catch(e){v(e instanceof Error?e.message:`Save failed.`)}}return(0,l.jsxs)(`div`,{className:`max-w-2xl space-y-6`,children:[!f&&(0,l.jsx)(r.Field,{label:`Worker ID`,description:`The stable UUID assigned to the worker on first connection. Find it on the Workers page when the worker is online, or in the worker's state-dir file.`,children:(0,l.jsx)(a,{value:p,onChange:e=>m(e.target.value),placeholder:`e.g. 7f3a1b2c-4d5e-6f7a-8b9c-0d1e2f3a4b5c`,className:`font-mono`})}),(0,l.jsxs)(r.Group,{layout:`stacked`,variant:`bordered`,title:`Templates`,children:[(0,l.jsx)(`p`,{className:`mb-2 text-xs text-muted-foreground`,children:`Start from a common pattern. Clicking a template overwrites the editor below.`}),(0,l.jsx)(`div`,{className:`flex flex-wrap gap-2`,children:u.map(e=>(0,l.jsx)(i,{type:`button`,variant:`outline`,size:`sm`,title:e.description,onClick:()=>g(e.yaml),children:e.label},e.label))})]}),(0,l.jsx)(r.Field,{label:`Pod Template`,description:`A partial PodTemplateSpec applied to every notebook dispatched to this worker. Manifest pod_template fields override conflicts; Piper controls container name, image, ports, and PVC mounts.`,children:(0,l.jsx)(o,{value:h,onChange:e=>g(e.target.value),className:`min-h-[22rem]`})}),_&&(0,l.jsx)(`p`,{className:`text-sm text-destructive`,children:_}),(0,l.jsxs)(`div`,{className:`flex gap-3`,children:[(0,l.jsx)(i,{onClick:x,disabled:b,children:b?`Saving…`:`Save policy`}),(0,l.jsx)(i,{variant:`ghost`,onClick:d,disabled:b,children:`Cancel`})]})]})}export{d as t};