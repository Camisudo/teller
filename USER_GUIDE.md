# Teller User Guide

This guide covers how to use Teller as an end user.

## Getting Started

### First Run

1. **Start Teller** by running the executable:
   - **Linux/macOS**: `./teller`
   - **Windows**: Double-click `teller.exe`

2. A browser window opens to `http://localhost:3000`. If not, open it manually.

3. **Initial Setup**:
   - On first run, an admin account is created.
   - The admin email is `admin@teller.local`.
   - A password is generated and displayed (or written to `teller-admin-password.txt`).
   - Log in with these credentials.

4. **Change the Admin Password** (recommended):
   - After logging in, click your user menu (top right).
   - Select "Account Settings" or "Change Password".
   - Enter a secure password and save.

5. **Delete the Password File** (security):
   - After logging in, delete `teller-admin-password.txt` from the folder where Teller is installed.
   - This prevents the password from being exposed.

## Navigation

The main app has a sidebar with links to:

- **Dashboard**: Overview of key metrics and your tasks.
- **Contacts**: Manage individuals and companies.
- **Deals**: Track sales opportunities and pipeline.
- **Tasks**: Create and manage action items.
- **Activity**: View a log of recent changes.
- **Reports**: Sales metrics and summaries.

## Contacts

### Adding a Contact

1. Click **Contacts** in the sidebar.
2. Click **New Contact** (top right).
3. Fill in the form:
   - **Type**: Choose "Individual" or "Company".
   - **Name**: First and last name (or company name).
   - **Email**: Contact email address.
   - **Phone**: Phone number (optional).
   - **Company**: Associated company (optional).
4. Click **Save**.

### Editing a Contact

1. Find the contact in the list.
2. Click the **Edit** icon (pencil) on the right side of the row.
3. Update the fields and click **Save**.

### Deleting a Contact

1. Find the contact in the list.
2. Click the **Delete** icon (trash) on the right side of the row.
3. Confirm the deletion.

### Filtering Contacts

- Use the **Search** box to find contacts by name or email.
- Use the **Filter by Contact Type** dropdown to show only individuals or companies.

### Adding Notes to a Contact

1. Open a contact (click on its name or the detail icon).
2. Scroll to the **Notes** section.
3. Click **Add Note** and type your message.
4. Click **Save**.

## Deals

### Adding a Deal

1. Click **Deals** in the sidebar.
2. Click **New Deal** (top right).
3. Fill in the form:
   - **Title**: Name of the opportunity.
   - **Contact**: Select a contact or company.
   - **Value**: Estimated deal amount.
   - **Stage**: Pipeline stage (Prospect, Qualified, Proposal, Negotiation, Won, Lost).
   - **Probability**: Estimated win probability (0–100%).
   - **Description**: Additional details (optional).
4. Click **Save**.

### Moving a Deal

The **Pipeline** view shows deals organized by stage in columns (Kanban-style).

1. Click and drag a deal card to move it to a different stage.
2. The stage is updated immediately.

### Filtering Deals

- **Min/Max Value**: Use the spinners to filter by deal value range.
- **Search**: Type to find deals by title or contact name.

### Closing a Deal

1. Move the deal to either the **Won** or **Lost** column.
2. The deal is marked as closed.

## Tasks

### Adding a Task

1. Click **Tasks** in the sidebar.
2. Click **New Task** (top right).
3. Fill in the form:
   - **Title**: Task description.
   - **Type**: Call, Email, Meeting, Follow-up, or Other.
   - **Status**: Open, In Progress, Completed, or Cancelled.
   - **Priority**: Low, Medium, or High.
   - **Assigned To**: Who is responsible (optional).
   - **Due Date**: When it's due (optional).
   - **Description**: Additional details (optional).
4. Click **Save**.

### Completing a Task

**Option 1**: Check the checkbox next to the task in the list.

**Option 2**: 
1. Click the task to open it.
2. Change the status to "Completed".
3. Click **Save**.

### Filtering Tasks

- **Status**: Show only Open, In Progress, Completed, or Cancelled tasks.
- **Priority**: Filter by Low, Medium, or High.
- **Search**: Find tasks by title.

### Bulk Actions

1. Check the checkbox next to one or more tasks.
2. Use the bulk action buttons:
   - **Mark as Complete**: Changes status to Completed.
   - **Delete**: Removes selected tasks.

## Activities & Reports

### Activity Log

The **Activity** page shows a chronological log of recent changes in the system:
- Contact created/updated
- Deal stage changed
- Task assigned/completed
- Notes added

**Filter by**:
- **Type**: Select which activities to show.
- **Date Range**: Choose a time period.

**Pagination**: Use Previous/Next to browse pages.

### Reports

The **Reports** page provides high-level metrics:

- **Contacts**: Total number of contacts.
- **Active Deals**: Number of deals not yet closed.
- **Pipeline Value**: Total value of active deals.
- **Closed This Month**: Deals won/lost in the current month.
- **Win Rate**: Percentage of closed deals that were won.
- **Pipeline by Stage**: Breakdown of deals by pipeline stage.

**Date Range**: Change the period (Last 7, 30, or 90 days) to see different metrics.

## Dashboard

The **Dashboard** shows:

- **Summary Metrics**: Contacts, deals, pipeline value, and win rate.
- **My Tasks**: Your open and in-progress tasks.
- **Recent Activities**: Latest changes in the system.

Refresh the dashboard to see the latest data.

## User Account

### Changing Your Password

1. Click your name or avatar in the top right.
2. Select **Change Password**.
3. Enter your current password, then your new password.
4. Click **Save**.

### Logging Out

1. Click your name or avatar in the top right.
2. Click **Log Out**.
3. You are redirected to the login page.

## Tips & Best Practices

### Organization

- **Use contact types**: Mark individuals and companies separately for easier filtering.
- **Assign deals to contacts**: Keeps context clear and improves reporting.
- **Assign tasks**: Delegate work and track accountability.
- **Tag deals**: Use stage and probability to organize your pipeline.

### Workflow

1. **Prospecting**: Create a contact, then a deal in the "Prospect" stage.
2. **Qualification**: Move the deal to "Qualified" and add a note with key findings.
3. **Proposal**: Move to "Proposal" and create tasks for follow-ups.
4. **Negotiation**: Track progress with task updates and notes.
5. **Close**: Move to "Won" or "Lost" with a closed reason in the notes.

### Data Quality

- **Email validation**: Ensure contact emails are correct to avoid lost communications.
- **Keep notes**: Add context to deals and tasks so team members understand the history.
- **Regular updates**: Update deal stages and task statuses frequently for accurate reporting.

## Troubleshooting

### I forgot my password

Ask an admin to reset your password via the admin panel or CLI:
```bash
./teller -reset-admin-password
```

### The app is slow or unresponsive

- Check your internet connection (or local network if Teller is remote).
- Restart Teller: Stop the process and start it again.
- Clear browser cache: Ctrl+Shift+Delete (Chrome/Firefox) or Cmd+Shift+Delete (Safari).

### A deal or task isn't showing

- Verify that it was not deleted by another user.
- Check filters: You may have hidden it with active search or filter settings.
- Refresh the page: Ctrl+R or Cmd+R.

### I can't log in

- Verify that Teller is running: `ps aux | grep teller` (Linux/macOS) or Task Manager (Windows).
- Check the email and password are correct.
- Reset your password (requires admin access).

## Support

For technical issues, contact your administrator or the Teller support team. Provide:
- Error messages (from the browser console or app)
- Steps to reproduce
- Browser and OS version
