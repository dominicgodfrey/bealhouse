import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import './index.css'
import { Health } from './routes/Health'
import { Home } from './routes/Home'

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from index.html')

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/health" element={<Health />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
)
