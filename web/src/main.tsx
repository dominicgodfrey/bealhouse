import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import './index.css'
import { Confirm } from './routes/Confirm'
import { Health } from './routes/Health'
import { Held } from './routes/Held'
import { Home } from './routes/Home'
import { Room } from './routes/Room'
import { Search } from './routes/Search'

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from index.html')

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <Routes>
        {/* The booking flow: search → results → room → confirm → hold. */}
        <Route path="/" element={<Home />} />
        <Route path="/search" element={<Search />} />
        <Route path="/rooms/:slug" element={<Room />} />
        <Route path="/book/:slug" element={<Confirm />} />
        <Route path="/bookings/:code" element={<Held />} />

        <Route path="/health" element={<Health />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
)
