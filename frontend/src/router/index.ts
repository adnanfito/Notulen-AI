import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'dashboard',
      component: () => import('@/views/DashboardView.vue'),
    },
    {
      path: '/meetings/new',
      name: 'new-meeting',
      component: () => import('@/views/RecordingView.vue'),
    },
    {
      path: '/meetings/:id',
      name: 'meeting-detail',
      // Shows the live recorder when this is the meeting being recorded, and
      // the finished meeting otherwise.
      component: () => import('@/views/MeetingRoute.vue'),
    },
    // Anything else is a mistyped URL, not an error worth a page of its own.
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

export default router
