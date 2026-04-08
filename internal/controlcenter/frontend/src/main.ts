import { mount } from 'svelte'
import '../public/alf-ui.css'
import '../public/alf-components.js'
import App from './App.svelte'

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
