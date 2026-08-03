import{n as e}from"./rolldown-runtime-CbXtAM7H.js";import{o as t,s as n}from"./vendor-codemirror-B5x_ktTv.js";import{N as r,P as i,m as a}from"./index-BoH6QGnZ.js";import{t as o}from"./yaml-mirror-BvlDRZ4z.js";import{n as s}from"./hooks-bY3GOLi-.js";var c=e(n(),1),l=t(),u=[{label:`GPU (NVIDIA)`,description:`Schedule notebooks on NVIDIA GPU nodes with the nvidia runtime.`,yaml:`spec:
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
`}];function d({workerId:e,initialYaml:t,onSaved:n,onCancel:d}){let f=!!e,[p,m]=(0,c.useState)(e??``),[h,g]=(0,c.useState)(t??``),[_,v]=(0,c.useState)(``),{mutateAsync:y,isPending:b}=s();async function x(){v(``);let e=p.trim();if(!e){v(`Worker ID is required.`);return}if(!h.trim()){v(`Pod template YAML is required.`);return}try{await y({workerId:e,yaml:h}),n(e)}catch(e){v(e instanceof Error?e.message:`Save failed.`)}}return(0,l.jsxs)(`div`,{className:`space-y-3`,children:[!f&&(0,l.jsxs)(`div`,{className:`space-y-1.5`,children:[(0,l.jsx)(a,{htmlFor:`pod-policy-worker-id`,className:`text-xs`,children:`Worker ID`}),(0,l.jsx)(`p`,{className:`text-xs text-muted-foreground`,children:`The stable UUID assigned to the worker on first connection. Find it on the Workers page when the worker is online, or in the worker's state-dir file.`}),(0,l.jsx)(r,{id:`pod-policy-worker-id`,value:p,onChange:e=>m(e.target.value),placeholder:`e.g. 7f3a1b2c-4d5e-6f7a-8b9c-0d1e2f3a4b5c`,className:`h-8 font-mono text-sm`})]}),(0,l.jsxs)(`div`,{className:`space-y-1.5 rounded-(--radius) border border-border p-3`,children:[(0,l.jsx)(a,{className:`text-xs`,children:`Templates`}),(0,l.jsx)(`p`,{className:`text-xs text-muted-foreground`,children:`Start from a common pattern. Clicking a template overwrites the editor below.`}),(0,l.jsx)(`div`,{className:`flex flex-wrap gap-2`,children:u.map(e=>(0,l.jsx)(i,{type:`button`,variant:`outline`,size:`sm`,title:e.description,onClick:()=>g(e.yaml),children:e.label},e.label))})]}),(0,l.jsxs)(`div`,{className:`space-y-1.5`,children:[(0,l.jsx)(a,{className:`text-xs`,children:`Pod Template`}),(0,l.jsx)(`p`,{className:`text-xs text-muted-foreground`,children:`A partial PodTemplateSpec applied to every notebook dispatched to this worker. Manifest pod_template fields override conflicts; Piper controls container name, image, ports, and PVC mounts.`}),(0,l.jsx)(o,{value:h,onChange:e=>g(e.target.value),className:`min-h-[22rem]`})]}),_&&(0,l.jsx)(`p`,{className:`text-sm text-destructive`,role:`alert`,children:_}),(0,l.jsxs)(`div`,{className:`flex justify-end gap-2 border-t border-border pt-(--designkit-panel-gap)`,children:[(0,l.jsx)(i,{variant:`outline`,size:`sm`,className:`h-8 text-xs`,onClick:d,disabled:b,children:`Cancel`}),(0,l.jsx)(i,{size:`sm`,className:`h-8 text-xs`,onClick:x,disabled:b,children:b?`Saving…`:`Save policy`})]})]})}export{d as t};