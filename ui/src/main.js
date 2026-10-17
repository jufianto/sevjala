import './app.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { connect } from './lib/live.svelte.js'

const target = document.getElementById('app')
target.textContent = ''
connect()
mount(App, { target })
