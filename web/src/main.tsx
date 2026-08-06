import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'

import './index.css'
import { Confirm } from './routes/Confirm'
import { Health } from './routes/Health'
import { Held } from './routes/Held'
import { Home } from './routes/Home'
import { Manage } from './routes/Manage'
import { Pay } from './routes/Pay'
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

        {/*
          Both keyed by booking code, unlike /book/:slug above, which is keyed
          by room. Paying is a step in the life of a booking, so it belongs
          beside the booking rather than beside the room that started it.
        */}
        <Route path="/bookings/:code" element={<Held />} />
        <Route path="/bookings/:code/pay" element={<Pay />} />

        {/*
          The manage-booking link from decision #19, and the one route a guest
          arrives at from outside the site. Singular "booking" so the address in
          an email reads as one stay rather than as a listing, and separate from
          /bookings/:code above because that one is the hold countdown and this
          one is a capability the token in the URL has to authorise.
        */}
        <Route path="/booking/:code" element={<Manage />} />

        <Route path="/health" element={<Health />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
)
