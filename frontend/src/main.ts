import { mount } from 'svelte'
import '@fontsource/gloock'
import '@fontsource/atkinson-hyperlegible/400.css'
import '@fontsource/atkinson-hyperlegible/700.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import './app.css'
import App from './App.svelte'

mount(App, { target: document.getElementById('app')! })
