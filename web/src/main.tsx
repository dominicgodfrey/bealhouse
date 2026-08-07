import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'

import './index.css'
import { Account } from './routes/admin/Account'
import { Console } from './routes/admin/Console'
import { Enroll } from './routes/admin/Enroll'
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

        {/*
          The owner's console (decision #15).

          /admin/enroll sits outside the gate on purpose: a phone accepting an
          invitation is by definition not signed in yet, and the single-use
          token in the fragment is what authorises it.

          Everything else is nested under <Console>, which gates on the session
          and renders the sign-in in place of the screen that was asked for
          rather than redirecting — so signing in lands where you were going.
        */}
        <Route path="/admin/enroll" element={<Enroll />} />
        <Route path="/admin" element={<Console />}>
          {/* Becomes Today once there is a today to show. */}
          <Route index element={<Navigate to="/admin/account" replace />} />
          <Route path="account" element={<Account />} />
        </Route>

        <Route path="/health" element={<Health />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
)
