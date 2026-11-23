Output o-csv.mp4

Set Shell "bash"
Set Width 1280
Set Height 760
Set FontSize 15
Set Padding 16
Set TypingSpeed 55ms

Hide
Type "o-csv docs/tutorials/fixtures/sales.csv"
Enter
Sleep 1500ms
Show

# Section: Navigate Rows, Columns, and Cells
# Open sales.csv in safe read-only VIEW mode showing all 20 rows and 7 columns.
Sleep 1400ms

# Jump down three rows at once with a count prefix (3j).
Type "3"
Type "j"
Sleep 1100ms

# Jump directly to the last row of the dataset.
Type "G"
Sleep 1100ms

# Jump back to the top row and move across five columns to amount.
Type "g"
Type "g"
Type "5"
Type "l"
Sleep 1200ms

# Jump to the last column to inspect the active cell's notes.
Type "$"
Sleep 1100ms

# Return to the first column with 0.
Type "0"
Sleep 1000ms

# Section: Filter Across All Columns or One Column
# Filter across every column for Cloud deals and press Enter to keep the results.
Type "/"
Type "Cloud"
Enter
Sleep 1400ms

# Clear the active filter with Esc to restore all 20 rows.
Escape
Sleep 1000ms

# Move to the status column before applying a column filter.
Type "4"
Type "l"
Sleep 400ms

# Filter only the status column using Tab to switch to regex mode and match open|pending.
Type "f"
Tab
Tab
Type "open|pending"
Enter
Sleep 1500ms

# Clear the column filter with Esc to restore all 20 rows while keeping focus on S-102.
Escape
Sleep 1000ms

# Cycle match mode back to substring and return to row 1.
Type "/"
Tab
Escape
Type "g"
Type "g"
Sleep 300ms

# Section: Sort Numerically and Group Rows
# Move to the numeric amount column.
Type "l"
Sleep 400ms

# Sort amount ascending so numeric values order from 4600 upward.
Type "s"
Sleep 1300ms

# Press s again to sort amount descending and bring the largest deals to the top.
Type "s"
Sleep 1300ms

# Press s a third time to restore the original file order.
Type "s"
Sleep 1000ms

# Move to the region column to group deals geographically.
Type "4"
Type "h"
Sleep 400ms

# Group rows by the active region column.
Type "g"
Type "b"
Sleep 1400ms

# Fold the focused region group to collapse its rows out of the way.
Type "z"
Type "a"
Sleep 1200ms

# Ungroup with g b to return to the flat table view.
Type "g"
Type "b"
Sleep 1000ms

# Section: Inspect Row Details, Manage Columns, and Yank
# Open the row detail pane to inspect every column of the focused row in full.
Enter
Sleep 1400ms

# Widen the grid pane next to the detail split.
Ctrl+W
Type ">"
Sleep 1100ms

# Hide the active region column from the grid while keeping it marked hidden in the detail pane.
Type "-"
Sleep 1300ms

# Restore all hidden columns at once.
Type "g"
Type "="
Sleep 1100ms

# Yank the active cell value to the register and clipboard.
Type "y"
Sleep 1100ms

# Yank the entire focused row as a CSV record.
Type "Y"
Sleep 1100ms

# Close the detail pane and move to the status column for editing.
Enter
Type "2"
Type "l"
Sleep 400ms

# Section: Edit Rows, Transform Cells, and Undo/Redo
# Switch from read-only VIEW mode into EDIT mode.
Ctrl+E
Sleep 1200ms

# Upper-case the active status cell in place.
Type "t"
Type "u"
Sleep 1200ms

# Title-case the active status cell.
Type "t"
Type "t"
Sleep 1100ms

# Open the validated row edit form, update amount to 26000, and apply.
Type "i"
Tab
Tab
Tab
Tab
Tab
Ctrl+U
Type "26000"
Enter
Sleep 1400ms

# Insert a blank row directly below the cursor.
Type "o"
Sleep 1200ms

# Delete the blank row with d d.
Type "d"
Type "d"
Sleep 1200ms

# Undo the row deletion with u.
Type "u"
Sleep 1200ms

# Redo the row deletion with Ctrl+R.
Ctrl+R
Sleep 1200ms

# Undo remaining edits back to the original clean document.
Type "u"
Type "u"
Type "u"
Type "u"
Type "u"
Sleep 400ms

# Return to safe read-only VIEW mode with Ctrl+E.
Ctrl+E
Sleep 1100ms

# Section: Live Key Help and Command Palette
# Open live help (?) generated directly from the active keymap.
Type "?"
Sleep 1500ms

# Close the help overlay.
Escape
Sleep 400ms

# Open the command palette (:) and filter for sort commands.
Type ":"
Type "sort"
Sleep 1500ms

# Close the command palette to return to the grid.
Escape
Sleep 600ms

