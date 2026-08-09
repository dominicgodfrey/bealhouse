import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'

import './index.css'
import { Account } from './routes/admin/Account'
import { BookingDetail } from './routes/admin/BookingDetail'
import { Bookings } from './routes/admin/Bookings'
import { CalendarScreen } from './routes/admin/CalendarScreen'
import { Collect } from './routes/admin/Collect'
import { Console } from './routes/admin/Console'
import { EmailCopy } from './routes/admin/EmailCopy'
import { Enroll } from './routes/admin/Enroll'
import { EventsEditor, Inquiries } from './routes/admin/EventsEditor'
import { GuestFile, Guests } from './routes/admin/Guests'
import { MenuEditor } from './routes/admin/MenuEditor'
import { NewBooking } from './routes/admin/NewBooking'
import { PageCopyEditor } from './routes/admin/PageCopyEditor'
import { Rates } from './routes/admin/Rates'
import { RoomsContent } from './routes/admin/RoomsContent'
import { SettingsScreen } from './routes/admin/SettingsScreen'
import { Today } from './routes/admin/Today'
import { Confirm } from './routes/Confirm'
import { Events } from './routes/Events'
import { Health } from './routes/Health'
import { Held } from './routes/Held'
import { Home } from './routes/Home'
import { LocalArea } from './routes/LocalArea'
import { Manage } from './routes/Manage'
import { Pay } from './routes/Pay'
import { Policies } from './routes/Policies'
import { Restaurant } from './routes/Restaurant'
import { Room } from './routes/Room'
import { Rooms } from './routes/Rooms'
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
        <Route path="/rooms" element={<Rooms />} />
        <Route path="/rooms/:slug" element={<Room />} />
        <Route path="/book/:slug" element={<Confirm />} />

        {/*
          The rest of the marketing site. Every word on these comes out of the
          console rather than out of this repository, so they are structure with
          the owner's content in it — and structure that says "not yet" rather
          than inventing a sentence when there is none.
        */}
        <Route path="/restaurant" element={<Restaurant />} />
        <Route path="/events" element={<Events />} />
        <Route path="/local-area" element={<LocalArea />} />

        {/*
          Indexable, unlike the booking flow it belongs to: a guest looking for
          the cancellation terms after they have booked must be able to find
          them, and being asked to agree to something unfindable is what this
          page exists to stop.
        */}
        <Route path="/policies" element={<Policies />} />

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
          {/* What is happening at the inn today, which is what anybody opening
              the console without a specific reason came to find out. */}
          <Route index element={<Navigate to="/admin/today" replace />} />
          <Route path="today" element={<Today />} />

          {/* Operations. */}
          <Route path="bookings" element={<Bookings />} />
          <Route path="bookings/new" element={<NewBooking />} />
          <Route path="bookings/:code" element={<BookingDetail />} />
          {/* Keying in a card the guest is reading out over the telephone. */}
          <Route path="bookings/:code/collect" element={<Collect />} />
          <Route path="calendar" element={<CalendarScreen />} />
          <Route path="guests" element={<Guests />} />
          <Route path="guests/:id" element={<GuestFile />} />
          <Route path="rates" element={<Rates />} />

          {/* Content — all of it the owner's. */}
          <Route path="rooms" element={<RoomsContent />} />
          <Route path="menu" element={<MenuEditor />} />
          <Route path="events" element={<EventsEditor />} />
          <Route path="inquiries" element={<Inquiries />} />
          <Route path="pages" element={<PageCopyEditor />} />
          <Route path="email" element={<EmailCopy />} />

          <Route path="settings" element={<SettingsScreen />} />
          <Route path="account" element={<Account />} />
        </Route>

        <Route path="/health" element={<Health />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
)
